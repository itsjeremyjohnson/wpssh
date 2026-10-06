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
	Args []string `arg:"" optional:"" help:"Arguments to pass to wp-cli. Forwards piped or redirected stdin to wp only for a \"-\" argument, and refuses that \"-\" when stdin is a terminal or empty or the command is wp db. Refuses shell operators outside quotes and commands that write dumps or exports on the server."`
}

func (c *RawCmd) Run(g *Globals) error {
	return c.runWithIO(g, os.Stdin)
}

func (c *RawCmd) runWithIO(g *Globals, stdin io.Reader) error {
	if err := checkRawArgs(c.Args, false); err != nil {
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
	nested := adapter.ForSite(site).Name() == "wpengine"
	if nested {
		if err := checkRawArgs(c.Args, true); err != nil {
			return err
		}
	}
	readsStdin, err := checkRawStdin(rawWordLayers(c.Args, nested))
	if err != nil {
		return err
	}

	// Build the raw command by passing all args through.
	builder := wpcli.New(c.Args...)
	cmd := builder.Build(site.WPPath)
	var result internalssh.ExecResult
	if readsStdin && !g.DryRun {
		input, err := rawInput(stdin)
		if err != nil {
			return err
		}
		result, err = rc.ExecWPWithStdin(context.Background(), site, cmd, input)
		if err != nil {
			return err
		}
	} else {
		result, err = rc.ExecWP(context.Background(), site, cmd)
		if err != nil {
			return err
		}
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

// rawWordLayers returns the argvs wp may receive for args: the words the
// remote shell passes and, with nested, as for WP Engine, the words the
// gateway's second parse makes of them. A word that fails the second parse
// is kept as it is; checkRawArgs has already refused it unless it is eval PHP.
func rawWordLayers(args []string, nested bool) [][]string {
	words, _ := shellWords(strings.Join(args, " "))
	layers := [][]string{words}
	if !nested {
		return layers
	}
	var inner []string
	for _, w := range words {
		ws, err := shellWords(w)
		if err != nil {
			ws = []string{w}
		}
		inner = append(inner, ws...)
	}
	return append(layers, inner)
}

// dbStdoutSubcommands are the wp db subcommands that read "-" as stdout.
var dbStdoutSubcommands = map[string]bool{"export": true, "dump": true}

// checkRawStdin reports whether any layer of argv holds a "-" word that wp
// reads from stdin; db export and dump write to stdout for "-" instead. It
// refuses every wp db command that would read SQL from stdin, as a "-" file
// or as db query without SQL, because raw checks SQL only in arguments.
func checkRawStdin(layers [][]string) (readsStdin bool, err error) {
	for _, words := range layers {
		dash := slices.Contains(words, "-")
		for _, r := range wpReadings {
			pos, flags := parseWPArgs(words, r)
			if len(pos) < 2 || pos[0] != "db" {
				continue
			}
			if dbStdoutSubcommands[pos[1]] {
				dash = false
				continue
			}
			if pos[1] == "query" && len(pos) == 2 && !slices.ContainsFunc(flags, func(f wpFlag) bool { return f.name == "execute" }) {
				return false, errors.New("wpgo raw refuses `wp db query` without SQL: wp would read SQL from stdin, which raw neither checks nor forwards; pass the SQL as an argument")
			}
		}
		if !dash {
			continue
		}
		for _, r := range wpReadings {
			if pos, _ := parseWPArgs(words, r); len(pos) > 0 && pos[0] == "db" {
				return false, errors.New(`wpgo raw refuses a "-" argument on wp db: SQL read from stdin would skip the checks raw applies to SQL arguments; pass the SQL as an argument or use ` + "`wpgo db import`")
			}
		}
		readsStdin = true
	}
	return readsStdin, nil
}

// rawInput returns local stdin for wp to read for a "-" argument. It refuses
// a terminal or empty stdin: wp would read EOF and write empty content, such
// as an empty post body.
func rawInput(stdin io.Reader) (io.Reader, error) {
	if !isPiped(stdin) {
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
