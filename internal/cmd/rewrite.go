package cmd

import "github.com/builtbyrobben/wpssh/internal/wpcli"

// Rewrite commands

type RewriteCmd struct {
	Flush     RewriteFlushCmd     `cmd:"" help:"Flush rewrite rules"`
	List      RewriteListCmd      `cmd:"" help:"List rewrite rules"`
	Structure RewriteStructureCmd `cmd:"" help:"Update permalink structure"`
}

type (
	RewriteFlushCmd     struct{}
	RewriteListCmd      struct{}
	RewriteStructureCmd struct {
		Permastruct string `arg:"" help:"Permalink structure"`
	}
)

func (c *RewriteFlushCmd) Run(g *Globals) error {
	return execPassthrough(g, wpcli.New("rewrite", "flush"))
}

func (c *RewriteListCmd) Run(g *Globals) error {
	return execPassthrough(g, wpcli.New("rewrite", "list"))
}

func (c *RewriteStructureCmd) Run(g *Globals) error {
	return execPassthrough(g, wpcli.New("rewrite", "structure"), c.Permastruct)
}
