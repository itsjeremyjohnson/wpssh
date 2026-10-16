package cmd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/builtbyrobben/wpssh/internal/cache"
	"github.com/builtbyrobben/wpssh/internal/wpcli"
)

// Option commands

type OptionCmd struct {
	Get    OptionGetCmd    `cmd:"" help:"Get option value"`
	Update OptionUpdateCmd `cmd:"" help:"Update option"`
	Add    OptionAddCmd    `cmd:"" help:"Add option"`
	Delete OptionDeleteCmd `cmd:"" help:"Delete option"`
	List   OptionListCmd   `cmd:"" help:"List options"`
	Patch  OptionPatchCmd  `cmd:"" help:"Patch option"`
	Pluck  OptionPluckCmd  `cmd:"" help:"Pluck option value"`
}

type OptionGetCmd struct {
	Key string `arg:"" help:"Option name"`
}
type OptionUpdateCmd struct {
	Key   string `arg:"" help:"Option name"`
	Value string `arg:"" help:"Option value"`
}
type OptionAddCmd struct {
	Key   string `arg:"" help:"Option name"`
	Value string `arg:"" help:"Option value"`
}
type OptionDeleteCmd struct {
	Key string `arg:"" help:"Option name"`
}
type OptionListCmd struct {
	Search string `help:"Search pattern"`
}
type OptionPatchCmd struct {
	Action  string `arg:"" help:"Patch action"`
	Key     string `arg:"" help:"Option name"`
	KeyPath string `arg:"" help:"Key path"`
	Value   string `arg:"" optional:"" help:"Value"`
}
type OptionPluckCmd struct {
	Key     string `arg:"" help:"Option name"`
	KeyPath string `arg:"" help:"Key path"`
}

func (c *OptionGetCmd) Run(g *Globals) error {
	return execPassthrough(g, wpcli.New("option", "get").Arg(c.Key))
}

func (c *OptionUpdateCmd) Run(g *Globals) error {
	return execValueWrite(g, valueWrite{
		cmd:        wpcli.New("option", "update"),
		script:     optionUpdatePHP,
		call:       "update_option",
		ids:        []string{"option name"},
		args:       []string{c.Key},
		value:      c.Value,
		invalidate: []string{cache.CategoryOptions},
	})
}

func (c *OptionAddCmd) Run(g *Globals) error {
	return execValueWrite(g, valueWrite{
		cmd:        wpcli.New("option", "add"),
		script:     optionAddPHP,
		call:       "add_option",
		ids:        []string{"option name"},
		args:       []string{c.Key},
		value:      c.Value,
		invalidate: []string{cache.CategoryOptions},
	})
}

func (c *OptionDeleteCmd) Run(g *Globals) error {
	rc, err := NewRunContext(g)
	if err != nil {
		return err
	}
	defer rc.Close()
	site, err := rc.ResolveSite()
	if err != nil {
		return err
	}

	result, err := rc.ExecWP(context.Background(), site,
		wpcli.New("option", "delete").Arg(c.Key).Build(site.WPPath))
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("wp option delete: %s", result.Stderr)
	}
	rc.CacheInvalidate(site.Alias, []string{cache.CategoryOptions})
	fmt.Fprint(rc.Stdout, result.Stdout)
	return nil
}

func (c *OptionListCmd) Run(g *Globals) error {
	rc, err := NewRunContext(g)
	if err != nil {
		return err
	}
	defer rc.Close()
	site, err := rc.ResolveSite()
	if err != nil {
		return err
	}

	builder := wpcli.New("option", "list").Format("json")
	if c.Search != "" {
		builder.Flag("search", c.Search)
	}
	cacheKey := builder.CacheKey()

	// Check cache first.
	if cached := rc.CacheGet(site.Alias, cacheKey); cached != "" {
		options, err := wpcli.ParseJSON[wpcli.Option](cached)
		if err == nil {
			return rc.Formatter.Format(options)
		}
	}

	result, err := rc.ExecWP(context.Background(), site, builder.Build(site.WPPath))
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("wp option list: %s", result.Stderr)
	}
	options, err := wpcli.ParseJSON[wpcli.Option](result.Stdout)
	if err != nil {
		return err
	}

	// Store in cache.
	if data, err := json.Marshal(options); err == nil {
		rc.CacheSet(site.Alias, cacheKey, "option list", string(data), cache.CategoryOptions, cache.TTLOptions)
	}

	return rc.Formatter.Format(options)
}

func (c *OptionPatchCmd) Run(g *Globals) error {
	rc, err := NewRunContext(g)
	if err != nil {
		return err
	}
	defer rc.Close()
	site, err := rc.ResolveSite()
	if err != nil {
		return err
	}

	builder := wpcli.New("option", "patch").Arg(c.Action).Arg(c.Key).Arg(c.KeyPath)
	if c.Value != "" {
		builder.ValueArg(c.Value)
	}
	result, err := rc.ExecWPCommand(context.Background(), site, builder)
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("wp option patch: %s", result.Stderr)
	}
	rc.CacheInvalidate(site.Alias, []string{cache.CategoryOptions})
	fmt.Fprint(rc.Stdout, result.Stdout)
	return nil
}

func (c *OptionPluckCmd) Run(g *Globals) error {
	return execPassthrough(g, wpcli.New("option", "pluck").Arg(c.Key).Arg(c.KeyPath))
}
