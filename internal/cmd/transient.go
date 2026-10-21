package cmd

import (
	"fmt"

	"github.com/builtbyrobben/wpssh/internal/wpcli"
)

// Transient commands

type TransientCmd struct {
	Get    TransientGetCmd    `cmd:"" help:"Get transient"`
	Set    TransientSetCmd    `cmd:"" help:"Set transient"`
	Delete TransientDeleteCmd `cmd:"" help:"Delete transient(s)"`
	List   TransientListCmd   `cmd:"" help:"List transients"`
}

type TransientGetCmd struct {
	Key string `arg:"" help:"Transient key"`
}
type TransientSetCmd struct {
	Key        string `arg:"" help:"Transient key"`
	Value      string `arg:"" help:"Transient value"`
	Expiration int    `arg:"" optional:"" help:"Expiration in seconds"`
}
type TransientDeleteCmd struct {
	Key     string `arg:"" optional:"" help:"Transient key"`
	All     bool   `help:"Delete all transients"`
	Expired bool   `help:"Delete expired transients"`
}
type TransientListCmd struct{}

func (c *TransientGetCmd) Run(g *Globals) error {
	return execPassthrough(g, wpcli.New("transient", "get").Arg(c.Key))
}

func (c *TransientSetCmd) Run(g *Globals) error {
	cmd := wpcli.New("transient", "set").Arg(c.Key).ValueArg(c.Value)
	if c.Expiration > 0 {
		cmd.Arg(fmt.Sprint(c.Expiration))
	}
	return execPassthrough(g, cmd)
}

func (c *TransientDeleteCmd) Run(g *Globals) error {
	if c.All {
		return execPassthrough(g, wpcli.New("transient", "delete").BoolFlag("all"))
	}
	if c.Expired {
		return execPassthrough(g, wpcli.New("transient", "delete").BoolFlag("expired"))
	}
	cmd := wpcli.New("transient", "delete")
	if c.Key != "" {
		cmd.Arg(c.Key)
	}
	return execPassthrough(g, cmd)
}

func (c *TransientListCmd) Run(g *Globals) error {
	return execPassthrough(g, wpcli.New("transient", "list"))
}
