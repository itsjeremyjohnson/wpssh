package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/builtbyrobben/wpssh/internal/adapter"
	"github.com/builtbyrobben/wpssh/internal/cache"
	internalssh "github.com/builtbyrobben/wpssh/internal/ssh"
	"github.com/builtbyrobben/wpssh/internal/wpcli"
)

// Raw command — pass-through escape hatch to wp-cli.

type RawCmd struct {
	Args []string `arg:"" optional:"" help:"Arguments to pass to wp-cli. Forwards piped or redirected stdin to wp; refuses a \"-\" argument when stdin is a terminal or empty. Refuses shell operators outside quotes and commands that write dumps or exports on the server."`
}

func (c *RawCmd) Run(g *Globals) error {
	return c.runWithIO(g, os.Stdin)
}

func (c *RawCmd) runWithIO(g *Globals, stdin io.Reader) error {
	if err := checkRawArgs(c.Args, false); err != nil {
		return err
	}
	input, err := rawInput(c.Args, stdin)
	if err != nil {
		return err
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
	if adapter.ForSite(site).Name() == "wpengine" {
		if err := checkRawArgs(c.Args, true); err != nil {
			return err
		}
	}

	// Build the raw command by passing all args through.
	builder := wpcli.New(c.Args...)
	cmd := builder.Build(site.WPPath)
	var result internalssh.ExecResult
	if input != nil {
		result, err = rc.ExecWPWithStdin(context.Background(), site, cmd, input)
	} else {
		result, err = rc.ExecWP(context.Background(), site, cmd)
	}
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("wp %s: %s", strings.Join(c.Args, " "), result.Stderr)
	}
	// Raw commands are untyped — invalidate all categories as a safety measure.
	rc.CacheInvalidate(site.Alias, []string{
		cache.CategoryPlugins, cache.CategoryThemes, cache.CategoryCore,
		cache.CategoryUsers, cache.CategoryOptions, cache.CategorySnapshot,
	})
	fmt.Fprint(rc.Stdout, result.Stdout)
	return nil
}

// rawInput returns what wp reads as stdin: local stdin when it is piped or
// redirected from a file, or nil when it is a terminal. When a "-" argument
// makes wp read its input from stdin, a terminal or empty stdin is refused:
// wp would read EOF and write empty content, such as an empty post body.
func rawInput(args []string, stdin io.Reader) (io.Reader, error) {
	if !isPiped(stdin) {
		stdin = nil
	}
	if !readsStdinArg(args) {
		return stdin, nil
	}
	if stdin == nil {
		return nil, errors.New(`wpgo raw refuses a "-" argument when stdin is a terminal: wp would read no input and write empty content; pipe or redirect the input, e.g. wpgo raw -- post update 7 - < content.html`)
	}
	buffered := bufio.NewReader(stdin)
	if _, err := buffered.Peek(1); err != nil {
		if err == io.EOF {
			return nil, errors.New(`wpgo raw refuses a "-" argument with empty stdin: wp would write empty content`)
		}
		return nil, fmt.Errorf("read stdin: %w", err)
	}
	return buffered, nil
}

// isPiped reports whether r is input to forward: a pipe, a file or another
// reader, rather than nil, a terminal or another character device such as
// /dev/null.
func isPiped(r io.Reader) bool {
	if r == nil {
		return false
	}
	f, ok := r.(*os.File)
	if !ok {
		return true
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice == 0
}

// dbStdoutSubcommands are the wp db subcommands that read "-" as stdout.
var dbStdoutSubcommands = map[string]bool{"export": true, "dump": true}

// readsStdinArg reports whether args hold a "-" word that wp reads from stdin.
// wp db export and dump write the dump to stdout for "-" instead.
func readsStdinArg(args []string) bool {
	words, err := shellWords(strings.Join(args, " "))
	if err != nil || !slices.Contains(words, "-") {
		return false
	}
	for _, r := range wpReadings {
		if pos, _ := parseWPArgs(words, r); len(pos) > 1 && pos[0] == "db" && dbStdoutSubcommands[pos[1]] {
			return false
		}
	}
	return true
}
