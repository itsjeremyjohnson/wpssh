package cmd

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/builtbyrobben/wpssh/internal/adapter"
	"github.com/builtbyrobben/wpssh/internal/registry"
	internalssh "github.com/builtbyrobben/wpssh/internal/ssh"
	"github.com/builtbyrobben/wpssh/internal/wpcli"
)

// valueWrite is a wp-cli write that stores value under its identifying
// arguments, e.g. post meta update ID KEY VALUE.
//
// WP Engine's SSH gateway parses the remote command a second time and drops
// its quoting, so a quoted value such as 'a b c' reaches wp as three words
// and an empty argument disappears. On WP Engine a valueWrite runs script
// from stdin with `wp eval-file -` instead and passes each argument as
// evalArg encodes it.
type valueWrite struct {
	cmd *wpcli.Command // wp-cli command words, e.g. post meta update
	// script is the PHP eval-file runs on WP Engine. It reads the encoded
	// args from $args and should report like cmd does.
	script string
	call   string // PHP function script calls, shown in dry-run output
	// ids names the identifying arguments that come before the value, e.g.
	// post ID and meta key. None may be empty.
	ids   []string
	args  []string // the identifying arguments, in wp-cli order
	value string   // stored value; verbose and dry-run output redact it
	// invalidate lists cache categories to invalidate after a write.
	invalidate []string
}

// execValueWrite runs w on the resolved site and prints its stdout.
func execValueWrite(g *Globals, w valueWrite) error {
	for i, a := range w.args {
		if a == "" {
			return fmt.Errorf("wp %s: %s must not be empty", w.cmd.Name(), w.ids[i])
		}
	}
	rc, err := NewRunContext(g)
	if err != nil {
		return err
	}
	defer rc.Close()
	site, err := rc.ResolveSite()
	if err != nil {
		return err
	}

	var result internalssh.ExecResult
	if adapter.ForSite(site).Name() == "wpengine" {
		result, err = rc.execEvalFile(context.Background(), site, w)
	} else {
		for _, a := range w.args {
			w.cmd.Arg(a)
		}
		result, err = rc.ExecWPCommand(context.Background(), site, w.cmd.ValueArg(w.value))
	}
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("wp %s: %s", w.cmd.Name(), result.Stderr)
	}
	if len(w.invalidate) > 0 {
		rc.CacheInvalidate(site.Alias, w.invalidate)
	}
	fmt.Fprint(rc.Stdout, result.Stdout)
	return nil
}

// evalArgPrefix starts every eval-file argument, so an empty value is still
// a word after WP Engine's gateway drops quoting.
const evalArgPrefix = "b64:"

// evalArg encodes s as one eval-file argument: evalArgPrefix and base64.
// Neither holds anything the gateway or the shell splits on or interprets.
func evalArg(s string) string {
	return evalArgPrefix + base64.StdEncoding.EncodeToString([]byte(s))
}

// execEvalFile runs w.script through `wp eval-file -` with w.args and
// w.value encoded by evalArg. Verbose and dry-run output show the identifying
// arguments and redact the value.
func (rc *RunContext) execEvalFile(ctx context.Context, site *registry.Site, w valueWrite) (internalssh.ExecResult, error) {
	cmd := wpcli.New("eval-file", "-")
	for _, a := range w.args {
		cmd.ArgShown(evalArg(a), fmt.Sprintf("<%s %q>", evalArgPrefix, a))
	}
	cmd.ArgShown(evalArg(w.value), wpcli.Redact(w.value))
	shown := cmd.Display(site.WPPath) + " < [PHP calling " + w.call + "]"
	return rc.execWPShown(ctx, site, cmd.Build(site.WPPath), shown, strings.NewReader(w.script))
}

// The eval-file scripts below select, sanitize, compare, and report like the
// wp-cli commands they replace (wp-cli's user fetcher, CommandWithMeta::update,
// and Option_Command).
// Each starts with evalArgsPHP, which decodes $args into $decoded and stops
// unless it holds exactly the expected number of evalArg words.

// evalArgsPHP decodes $args. Prepend it to a script that sets $want first.
const evalArgsPHP = `
if ( count( $args ) !== $want ) {
	WP_CLI::error( sprintf( 'Expected %d arguments, got %d.', $want, count( $args ) ) );
}
$prefix  = '` + evalArgPrefix + `';
$decoded = array();
foreach ( $args as $arg ) {
	$raw = 0 === strpos( $arg, $prefix ) ? base64_decode( substr( $arg, strlen( $prefix ) ), true ) : false;
	if ( false === $raw ) {
		WP_CLI::error( 'Malformed argument.' );
	}
	$decoded[] = $raw;
}
`

const postMetaUpdatePHP = `<?php
$want = 3;` + evalArgsPHP + `
list( $id, $key, $value ) = $decoded;
$post = get_post( (int) $id );
if ( ! $post ) {
	WP_CLI::error( "Could not find the post with ID {$id}." );
}
$value = sanitize_meta( $key, $value, 'post' );
if ( sanitize_meta( $key, get_post_meta( $post->ID, $key, true ), 'post' ) === $value ) {
	WP_CLI::success( "Value passed for custom field '{$key}' is unchanged." );
} elseif ( update_post_meta( $post->ID, wp_slash( $key ), wp_slash( $value ) ) ) {
	WP_CLI::success( "Updated custom field '{$key}'." );
} else {
	WP_CLI::error( "Failed to update custom field '{$key}'." );
}
`

const userMetaUpdatePHP = `<?php
$want = 3;` + evalArgsPHP + `
list( $user, $key, $value ) = $decoded;
// Select the user as wp-cli's user fetcher does.
$invalid = "Invalid user ID, email or login: '{$user}'";
if ( getenv( 'WP_CLI_FORCE_USER_LOGIN' ) ) {
	$invalid = "Invalid user login: '{$user}'";
	$found   = get_user_by( 'login', $user );
} elseif ( is_numeric( $user ) ) {
	$found = get_user_by( 'id', $user );
	if ( $found && get_user_by( 'login', $user ) ) {
		WP_CLI::warning(
			sprintf(
				'Ambiguous user match detected (both ID and user_login exist for identifier \'%d\'). WP-CLI will default to the ID, but you can force user_login instead with WP_CLI_FORCE_USER_LOGIN=1.',
				(int) $user
			)
		);
	}
} elseif ( is_email( $user ) ) {
	$found = get_user_by( 'email', $user );
	if ( ! $found ) {
		$found = get_user_by( 'login', $user );
	}
} else {
	$found = get_user_by( 'login', $user );
}
if ( ! $found ) {
	WP_CLI::error( $invalid );
}
$value = sanitize_meta( $key, $value, 'user' );
if ( sanitize_meta( $key, get_user_meta( $found->ID, $key, true ), 'user' ) === $value ) {
	WP_CLI::success( "Value passed for custom field '{$key}' is unchanged." );
} elseif ( update_user_meta( $found->ID, wp_slash( $key ), wp_slash( $value ) ) ) {
	WP_CLI::success( "Updated custom field '{$key}'." );
} else {
	WP_CLI::error( "Failed to update custom field '{$key}'." );
}
`

const optionUpdatePHP = `<?php
$want = 2;` + evalArgsPHP + `
list( $key, $value ) = $decoded;
$value = sanitize_option( $key, $value );
if ( in_array( $key, array( 'siteurl', 'home', 'category_base', 'tag_base' ), true ) ) {
	$value = untrailingslashit( $value );
}
if ( sanitize_option( $key, get_option( $key ) ) === $value ) {
	WP_CLI::success( "Value passed for '{$key}' option is unchanged." );
} elseif ( update_option( $key, $value ) ) {
	WP_CLI::success( "Updated '{$key}' option." );
} else {
	WP_CLI::error( "Could not update option '{$key}'." );
}
`

const optionAddPHP = `<?php
$want = 2;` + evalArgsPHP + `
list( $key, $value ) = $decoded;
if ( ! add_option( $key, $value ) ) {
	WP_CLI::error( "Could not add option '{$key}'. Does it already exist?" );
}
WP_CLI::success( "Added '{$key}' option." );
`
