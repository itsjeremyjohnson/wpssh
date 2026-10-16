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

// valueWrite is a wp-cli write that stores its last argument as a value.
//
// WP Engine's SSH gateway parses the remote command a second time and drops
// its quoting, so a quoted value such as 'a b c' reaches wp as three words.
// On WP Engine a valueWrite runs script from stdin with `wp eval-file -`
// instead and passes each argument base64-encoded; the base64 alphabet holds
// nothing that either parse splits on or interprets.
type valueWrite struct {
	cmd *wpcli.Command // wp-cli command words, e.g. post meta update
	// script is the PHP eval-file runs on WP Engine. It reads the
	// base64-encoded args from $args and should report like cmd does.
	script string
	call   string   // PHP function script calls, shown in dry-run output
	args   []string // positional arguments; the last is the stored value
	// invalidate lists cache categories to invalidate after a write.
	invalidate []string
}

// execValueWrite runs w on the resolved site and prints its stdout.
func execValueWrite(g *Globals, w valueWrite) error {
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
		result, err = rc.ExecWP(context.Background(), site, w.cmd.Build(site.WPPath))
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

// execEvalFile runs w.script through `wp eval-file -` with w.args
// base64-encoded. Verbose and dry-run output shows the arguments before the
// value and the value's length, but not the value or its encoding.
func (rc *RunContext) execEvalFile(ctx context.Context, site *registry.Site, w valueWrite) (internalssh.ExecResult, error) {
	cmd := wpcli.New("eval-file", "-")
	shown := []string{cmd.Build(site.WPPath)}
	for i, a := range w.args {
		cmd.Arg(base64.StdEncoding.EncodeToString([]byte(a)))
		if i == len(w.args)-1 {
			shown = append(shown, fmt.Sprintf("<base64 value, %d bytes>", len(a)))
		} else {
			shown = append(shown, fmt.Sprintf("<base64 %q>", a))
		}
	}
	shown = append(shown, "< [PHP calling "+w.call+"]")

	a := adapter.ForSite(site)
	if rc.Globals.Verbose {
		fmt.Fprintf(rc.Stderr, "[adapter: %s] %s\n", a.Name(), strings.Join(shown, " "))
	}
	if rc.Globals.DryRun {
		fmt.Fprintf(rc.Stderr, "[dry-run] %s\n", strings.Join(shown, " "))
		return internalssh.ExecResult{}, nil
	}
	return a.ExecStdin(ctx, rc.SSHClient, site, cmd.Build(site.WPPath), strings.NewReader(w.script))
}

// The eval-file scripts below report like the wp-cli commands they replace.
// An empty last argument encodes to an empty word, which WP Engine's gateway
// drops, so each script pads $args before decoding.

const postMetaUpdatePHP = `<?php
list( $id, $key, $value ) = array_map( 'base64_decode', array_pad( $args, 3, '' ) );
$post = get_post( (int) $id );
if ( ! $post ) {
	WP_CLI::error( "Could not find the post with ID {$id}." );
}
if ( get_post_meta( $post->ID, $key, true ) === $value ) {
	WP_CLI::success( "Value passed for custom field '{$key}' is unchanged." );
} elseif ( update_post_meta( $post->ID, wp_slash( $key ), wp_slash( $value ) ) ) {
	WP_CLI::success( "Updated custom field '{$key}'." );
} else {
	WP_CLI::error( "Failed to update custom field '{$key}'." );
}
`

const userMetaUpdatePHP = `<?php
list( $user, $key, $value ) = array_map( 'base64_decode', array_pad( $args, 3, '' ) );
$found = false;
if ( is_numeric( $user ) ) {
	$found = get_user_by( 'id', (int) $user );
} elseif ( is_email( $user ) ) {
	$found = get_user_by( 'email', $user );
}
if ( ! $found ) {
	$found = get_user_by( 'login', $user );
}
if ( ! $found ) {
	WP_CLI::error( "Invalid user ID, email or login: '{$user}'" );
}
if ( get_user_meta( $found->ID, $key, true ) === $value ) {
	WP_CLI::success( "Value passed for custom field '{$key}' is unchanged." );
} elseif ( update_user_meta( $found->ID, wp_slash( $key ), wp_slash( $value ) ) ) {
	WP_CLI::success( "Updated custom field '{$key}'." );
} else {
	WP_CLI::error( "Failed to update custom field '{$key}'." );
}
`

const optionUpdatePHP = `<?php
list( $key, $value ) = array_map( 'base64_decode', array_pad( $args, 2, '' ) );
$value = sanitize_option( $key, $value );
if ( sanitize_option( $key, get_option( $key ) ) === $value ) {
	WP_CLI::success( "Value passed for '{$key}' option is unchanged." );
} elseif ( update_option( $key, $value ) ) {
	WP_CLI::success( "Updated '{$key}' option." );
} else {
	WP_CLI::error( "Could not update option '{$key}'." );
}
`

const optionAddPHP = `<?php
list( $key, $value ) = array_map( 'base64_decode', array_pad( $args, 2, '' ) );
if ( ! add_option( $key, $value ) ) {
	WP_CLI::error( "Could not add option '{$key}'. Does it already exist?" );
}
WP_CLI::success( "Added '{$key}' option." );
`
