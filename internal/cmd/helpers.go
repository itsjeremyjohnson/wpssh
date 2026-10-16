package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/builtbyrobben/wpssh/internal/wpcli"
)

// execPassthrough runs cmd with args as escaped positional arguments and
// prints its stdout directly. Empty args are omitted (allows optional args).
func execPassthrough(g *Globals, cmd *wpcli.Command, args ...string) error {
	rc, err := NewRunContext(g)
	if err != nil {
		return err
	}
	defer rc.Close()

	site, err := rc.ResolveSite()
	if err != nil {
		return err
	}

	for _, a := range args {
		if a != "" {
			cmd.Arg(a)
		}
	}

	result, err := rc.ExecWP(context.Background(), site, cmd.Build(site.WPPath))
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("wp %s: %s", cmd.Name(), result.Stderr)
	}
	fmt.Fprint(rc.Stdout, result.Stdout)
	return nil
}

// execBoolCheck runs a wp-cli boolean check command (is-active, is-installed, exists)
// and prints a human-readable result.
func execBoolCheck(g *Globals, group, subCmd wpcli.Word, target, label string) error {
	rc, err := NewRunContext(g)
	if err != nil {
		return err
	}
	defer rc.Close()

	site, err := rc.ResolveSite()
	if err != nil {
		return err
	}

	cmd := wpcli.New(group, subCmd).Arg(target).Build(site.WPPath)
	result, err := rc.ExecWP(context.Background(), site, cmd)
	if err != nil {
		return err
	}

	verb := strings.ReplaceAll(string(subCmd), "-", " ")
	if result.ExitCode != 0 {
		fmt.Fprintf(rc.Stdout, "%s %s is not %s.\n", label, target, verb)
	} else {
		fmt.Fprintf(rc.Stdout, "%s %s is %s.\n", label, target, verb)
	}
	return nil
}
