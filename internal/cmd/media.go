package cmd

import (
	"context"
	"fmt"

	"github.com/builtbyrobben/wpssh/internal/wpcli"
)

// Media commands

type MediaCmd struct {
	Import     MediaImportCmd     `cmd:"" help:"Import media"`
	Regenerate MediaRegenerateCmd `cmd:"" help:"Regenerate thumbnails"`
	ImageSize  MediaImageSizeCmd  `cmd:"" name:"image-size" help:"List image sizes"`
}

type MediaImportCmd struct {
	File string `arg:"" help:"File or URL"`
}
type MediaRegenerateCmd struct {
	ID  int  `arg:"" optional:"" help:"Attachment ID"`
	All bool `help:"Regenerate all"`
}
type MediaImageSizeCmd struct{}

func (c *MediaImportCmd) Run(g *Globals) error {
	return execPassthrough(g, wpcli.New("media", "import"), c.File)
}

func (c *MediaRegenerateCmd) Run(g *Globals) error {
	rc, err := NewRunContext(g)
	if err != nil {
		return err
	}
	defer rc.Close()
	site, err := rc.ResolveSite()
	if err != nil {
		return err
	}

	builder := wpcli.New("media", "regenerate").BoolFlag("yes")
	if c.All || c.ID == 0 {
		// no positional arg needed, wp-cli regenerates all
	} else {
		builder.Arg(fmt.Sprint(c.ID))
	}
	result, err := rc.ExecWP(context.Background(), site, builder.Build(site.WPPath))
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("wp media regenerate: %s", result.Stderr)
	}
	fmt.Fprint(rc.Stdout, result.Stdout)
	return nil
}

func (c *MediaImageSizeCmd) Run(g *Globals) error {
	return execPassthrough(g, wpcli.New("media", "image-size"))
}
