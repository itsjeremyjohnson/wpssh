package cmd

import "github.com/builtbyrobben/wpssh/internal/wpcli"

// Cache commands (wp cache — object cache operations)

type CacheCmdGroup struct {
	Flush  CacheFlushCmd  `cmd:"" help:"Flush object cache"`
	Get    CacheGetCmd    `cmd:"" help:"Get cached value"`
	Set    CacheSetCmd    `cmd:"" help:"Set cached value"`
	Delete CacheDeleteCmd `cmd:"" help:"Delete cached value"`
	Type   CacheTypeCmd   `cmd:"" help:"Show cache type"`
}

type (
	CacheFlushCmd struct{}
	CacheGetCmd   struct {
		Key   string `arg:"" help:"Cache key"`
		Group string `arg:"" optional:"" help:"Cache group"`
	}
)

type CacheSetCmd struct {
	Key   string `arg:"" help:"Cache key"`
	Value string `arg:"" help:"Cache value"`
	Group string `arg:"" optional:"" help:"Cache group"`
}
type CacheDeleteCmd struct {
	Key   string `arg:"" help:"Cache key"`
	Group string `arg:"" optional:"" help:"Cache group"`
}
type CacheTypeCmd struct{}

func (c *CacheFlushCmd) Run(g *Globals) error {
	return execPassthrough(g, wpcli.New("cache", "flush"))
}

func (c *CacheGetCmd) Run(g *Globals) error {
	return execPassthrough(g, wpcli.New("cache", "get"), c.Key, c.Group)
}

func (c *CacheSetCmd) Run(g *Globals) error {
	return execPassthrough(g, wpcli.New("cache", "set"), c.Key, c.Value, c.Group)
}

func (c *CacheDeleteCmd) Run(g *Globals) error {
	return execPassthrough(g, wpcli.New("cache", "delete"), c.Key, c.Group)
}

func (c *CacheTypeCmd) Run(g *Globals) error {
	return execPassthrough(g, wpcli.New("cache", "type"))
}
