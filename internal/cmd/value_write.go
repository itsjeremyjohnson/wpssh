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
// and an empty argument disappears. On WP Engine a valueWrite runs
// runCommandPHP from stdin with `wp eval-file -` instead, passing the command
// words and each argument as evalArg encodes them, and the native wp-cli
// command runs inside that process.
type valueWrite struct {
	cmd *wpcli.Command // wp-cli command words, e.g. post meta update
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

// execEvalFile runs w through `wp eval-file -` and runCommandPHP, with the
// command words, w.args and w.value encoded by evalArg. Verbose and dry-run
// output show the command words and identifying arguments and redact the
// value.
func (rc *RunContext) execEvalFile(ctx context.Context, site *registry.Site, w valueWrite) (internalssh.ExecResult, error) {
	cmd := wpcli.New("eval-file", "-")
	words := strings.Fields(w.cmd.Name())
	for _, word := range words {
		cmd.ArgShown(evalArg(word), word)
	}
	for _, a := range w.args {
		cmd.ArgShown(evalArg(a), fmt.Sprintf("<%s %q>", evalArgPrefix, a))
	}
	cmd.ArgShown(evalArg(w.value), wpcli.Redact(w.value))
	shown := cmd.Display(site.WPPath) + " < [PHP running the decoded wp command]"
	script := fmt.Sprintf(runCommandPHP, len(words)+len(w.args)+1)
	return rc.execWPShown(ctx, site, cmd.Build(site.WPPath), shown, strings.NewReader(script))
}

// runCommandPHP decodes its evalArg arguments and runs them as a wp-cli
// command in the eval-file process, so the native command selects,
// sanitizes, writes, and reports. It parses the words as wp-cli parses its
// own argv and runs them as WP_CLI::runcommand does in-process: a value such
// as --x is a flag there too. It refuses a global parameter such as --quiet,
// which wp-cli applies at startup but run_command cannot. It runs nothing
// unless it holds exactly as many words as execEvalFile sent, so a word the
// gateway dropped cannot shift the value into an identifying argument or
// leave the command to read its value from stdin.
const runCommandPHP = `<?php
if ( count( $args ) !== %d ) {
	WP_CLI::error( 'Unexpected argument count.' );
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
list( $positional, $assoc, $runtime ) = WP_CLI::get_configurator()->parse_args( $decoded );
if ( $runtime ) {
	WP_CLI::error( 'Global parameters are not supported: --' . implode( ', --', array_keys( $runtime ) ) );
}
WP_CLI::get_runner()->run_command( $positional, $assoc, array( 'back_compat_conversions' => true ) );
`
