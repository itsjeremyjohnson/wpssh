package cmd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/builtbyrobben/wpssh/internal/cache"
	"github.com/builtbyrobben/wpssh/internal/wpcli"
)

// Theme commands

type ThemeCmd struct {
	List        ThemeListCmd        `cmd:"" help:"List themes"`
	Install     ThemeInstallCmd     `cmd:"" help:"Install a theme"`
	Activate    ThemeActivateCmd    `cmd:"" help:"Activate a theme"`
	Delete      ThemeDeleteCmd      `cmd:"" help:"Delete a theme"`
	Update      ThemeUpdateCmd      `cmd:"" help:"Update theme(s)"`
	Search      ThemeSearchCmd      `cmd:"" help:"Search WordPress.org"`
	Get         ThemeGetCmd         `cmd:"" help:"Get theme details"`
	IsActive    ThemeIsActiveCmd    `cmd:"" name:"is-active" help:"Check if active"`
	IsInstalled ThemeIsInstalledCmd `cmd:"" name:"is-installed" help:"Check if installed"`
	Status      ThemeStatusCmd      `cmd:"" help:"Show theme status"`
	AutoUpdates ThemeAutoUpdatesCmd `cmd:"" name:"auto-updates" help:"Manage auto-updates"`
}

type ThemeListCmd struct {
	StatusFilter string `help:"Filter by status" name:"status"`
}
type ThemeInstallCmd struct {
	Theme    string `arg:"" help:"Theme slug"`
	Activate bool   `help:"Activate after install"`
}
type ThemeActivateCmd struct {
	Theme string `arg:"" help:"Theme slug"`
}
type ThemeDeleteCmd struct {
	Theme string `arg:"" help:"Theme slug"`
}
type ThemeUpdateCmd struct {
	Theme string `arg:"" optional:"" help:"Theme slug"`
	All   bool   `help:"Update all themes"`
}
type ThemeSearchCmd struct {
	Term string `arg:"" help:"Search term"`
}
type ThemeGetCmd struct {
	Theme string `arg:"" help:"Theme slug"`
}
type ThemeIsActiveCmd struct {
	Theme string `arg:"" help:"Theme slug"`
}
type ThemeIsInstalledCmd struct {
	Theme string `arg:"" help:"Theme slug"`
}
type ThemeStatusCmd struct {
	Theme string `arg:"" optional:"" help:"Theme slug"`
}
type ThemeAutoUpdatesCmd struct {
	Enable  ThemeAutoUpdatesEnableCmd  `cmd:"" help:"Enable auto-updates"`
	Disable ThemeAutoUpdatesDisableCmd `cmd:"" help:"Disable auto-updates"`
	Status  ThemeAutoUpdatesStatusCmd  `cmd:"" help:"Show auto-update status"`
}
type ThemeAutoUpdatesEnableCmd struct {
	Theme string `arg:"" help:"Theme slug"`
}
type ThemeAutoUpdatesDisableCmd struct {
	Theme string `arg:"" help:"Theme slug"`
}
type ThemeAutoUpdatesStatusCmd struct {
	Theme string `arg:"" optional:"" help:"Theme slug"`
}

func (c *ThemeListCmd) Run(g *Globals) error {
	rc, err := NewRunContext(g)
	if err != nil {
		return err
	}
	defer rc.Close()

	site, err := rc.ResolveSite()
	if err != nil {
		return err
	}

	builder := wpcli.New("theme", "list").Format("json")
	if c.StatusFilter != "" {
		builder.Flag("status", c.StatusFilter)
	}
	cacheKey := builder.CacheKey()

	// Check cache first.
	if cached := rc.CacheGet(site.Alias, cacheKey); cached != "" {
		themes, err := wpcli.ParseJSON[wpcli.Theme](cached)
		if err == nil {
			return rc.Formatter.Format(themes)
		}
	}

	cmd := builder.Build(site.WPPath)
	result, err := rc.ExecWP(context.Background(), site, cmd)
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("wp theme list: %s", result.Stderr)
	}

	themes, err := wpcli.ParseJSON[wpcli.Theme](result.Stdout)
	if err != nil {
		return err
	}

	// Store in cache.
	if data, err := json.Marshal(themes); err == nil {
		rc.CacheSet(site.Alias, cacheKey, "theme list", string(data), cache.CategoryThemes, cache.TTLThemes)
	}

	return rc.Formatter.Format(themes)
}

func (c *ThemeInstallCmd) Run(g *Globals) error {
	rc, err := NewRunContext(g)
	if err != nil {
		return err
	}
	defer rc.Close()
	site, err := rc.ResolveSite()
	if err != nil {
		return err
	}

	builder := wpcli.New("theme", "install").Arg(c.Theme)
	if c.Activate {
		builder.BoolFlag("activate")
	}
	result, err := rc.ExecWP(context.Background(), site, builder.Build(site.WPPath))
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("wp theme install: %s", result.Stderr)
	}
	rc.CacheInvalidate(site.Alias, []string{cache.CategoryThemes, cache.CategorySnapshot})
	fmt.Fprintln(rc.Stdout, result.Stdout)
	return nil
}

func (c *ThemeActivateCmd) Run(g *Globals) error {
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
		wpcli.New("theme", "activate").Arg(c.Theme).Build(site.WPPath))
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("wp theme activate: %s", result.Stderr)
	}
	rc.CacheInvalidate(site.Alias, []string{cache.CategoryThemes, cache.CategorySnapshot})
	fmt.Fprintln(rc.Stdout, result.Stdout)
	return nil
}

func (c *ThemeDeleteCmd) Run(g *Globals) error {
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
		wpcli.New("theme", "delete").Arg(c.Theme).Build(site.WPPath))
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("wp theme delete: %s", result.Stderr)
	}
	rc.CacheInvalidate(site.Alias, []string{cache.CategoryThemes, cache.CategorySnapshot})
	fmt.Fprintln(rc.Stdout, result.Stdout)
	return nil
}

func (c *ThemeUpdateCmd) Run(g *Globals) error {
	rc, err := NewRunContext(g)
	if err != nil {
		return err
	}
	defer rc.Close()
	site, err := rc.ResolveSite()
	if err != nil {
		return err
	}

	builder := wpcli.New("theme", "update")
	if c.All {
		builder.BoolFlag("all")
	} else if c.Theme != "" {
		builder.Arg(c.Theme)
	}
	result, err := rc.ExecWP(context.Background(), site, builder.Build(site.WPPath))
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("wp theme update: %s", result.Stderr)
	}
	rc.CacheInvalidate(site.Alias, []string{cache.CategoryThemes, cache.CategorySnapshot})
	fmt.Fprintln(rc.Stdout, result.Stdout)
	return nil
}

func (c *ThemeSearchCmd) Run(g *Globals) error {
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
		wpcli.New("theme", "search").Arg(c.Term).Format("json").Build(site.WPPath))
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("wp theme search: %s", result.Stderr)
	}
	themes, err := wpcli.ParseJSON[wpcli.Theme](result.Stdout)
	if err != nil {
		return err
	}
	return rc.Formatter.Format(themes)
}

func (c *ThemeGetCmd) Run(g *Globals) error {
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
		wpcli.New("theme", "get").Arg(c.Theme).Format("json").Build(site.WPPath))
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("wp theme get: %s", result.Stderr)
	}
	theme, err := wpcli.ParseSingle[wpcli.Theme](result.Stdout)
	if err != nil {
		return err
	}
	return rc.Formatter.Format(theme)
}

func (c *ThemeIsActiveCmd) Run(g *Globals) error {
	return execBoolCheck(g, "theme", "is-active", c.Theme, "Theme")
}

func (c *ThemeIsInstalledCmd) Run(g *Globals) error {
	return execBoolCheck(g, "theme", "is-installed", c.Theme, "Theme")
}

func (c *ThemeStatusCmd) Run(g *Globals) error {
	cmd := wpcli.New("theme", "status")
	if c.Theme != "" {
		cmd.Arg(c.Theme)
	}
	return execPassthrough(g, cmd)
}

func (c *ThemeAutoUpdatesEnableCmd) Run(g *Globals) error {
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
		wpcli.New("theme", "auto-updates", "enable").Arg(c.Theme).Build(site.WPPath))
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("wp theme auto-updates enable: %s", result.Stderr)
	}
	rc.CacheInvalidate(site.Alias, []string{cache.CategoryThemes})
	fmt.Fprintln(rc.Stdout, result.Stdout)
	return nil
}

func (c *ThemeAutoUpdatesDisableCmd) Run(g *Globals) error {
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
		wpcli.New("theme", "auto-updates", "disable").Arg(c.Theme).Build(site.WPPath))
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("wp theme auto-updates disable: %s", result.Stderr)
	}
	rc.CacheInvalidate(site.Alias, []string{cache.CategoryThemes})
	fmt.Fprintln(rc.Stdout, result.Stdout)
	return nil
}

func (c *ThemeAutoUpdatesStatusCmd) Run(g *Globals) error {
	cmd := wpcli.New("theme", "auto-updates", "status")
	if c.Theme != "" {
		cmd.Arg(c.Theme)
	}
	return execPassthrough(g, cmd)
}
