package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/builtbyrobben/wpssh/internal/cache"
	"github.com/builtbyrobben/wpssh/internal/wpcli"
)

// Raw command — pass-through escape hatch to wp-cli.

type RawCmd struct {
	Args []string `arg:"" optional:"" help:"Arguments to pass to wp-cli, each passed as one quoted word. Refuses commands that write dumps or exports on the server."`
}

func (c *RawCmd) Run(g *Globals) error {
	if err := checkRawArgs(c.Args); err != nil {
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

	cmd := rawCommand(site.WPPath, c.Args)
	result, err := rc.ExecWP(context.Background(), site, cmd)
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

// rawCommand quotes each arg so the remote shell passes wp exactly the argv
// that checkRawArgs approved, with no expansion, redirection or chaining.
func rawCommand(wpPath string, args []string) string {
	builder := wpcli.New()
	for _, a := range args {
		builder.Arg(a)
	}
	return builder.Build(wpPath)
}
