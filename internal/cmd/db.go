package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/builtbyrobben/wpssh/internal/cache"
	"github.com/builtbyrobben/wpssh/internal/wpcli"
)

// DB commands

type DBCmd struct {
	Export   DBExportCmd   `cmd:"" help:"Export database"`
	Import   DBImportCmd   `cmd:"" help:"Import database"`
	Query    DBQueryCmd    `cmd:"" help:"Run SQL query"`
	Search   DBSearchCmd   `cmd:"" help:"Search database"`
	Size     DBSizeCmd     `cmd:"" help:"Show database size"`
	Tables   DBTablesCmd   `cmd:"" help:"List tables"`
	Prefix   DBPrefixCmd   `cmd:"" help:"Show table prefix"`
	Check    DBCheckCmd    `cmd:"" help:"Check database"`
	Repair   DBRepairCmd   `cmd:"" help:"Repair database"`
	Optimize DBOptimizeCmd `cmd:"" help:"Optimize database"`
	Reset    DBResetCmd    `cmd:"" help:"Reset database"`
}

type DBExportCmd struct {
	File string `arg:"" optional:"" help:"Local file on this machine (default $WPGO_LOCAL_BACKUP_DIR/<site>/, else ~/wpgo-backups/<site>/); '-' streams to stdout. Nothing is written on the server."`
}
type DBImportCmd struct {
	File string `arg:"" help:"Import file path"`
}
type DBQueryCmd struct {
	SQL string `arg:"" help:"SQL query"`
}
type DBSearchCmd struct {
	Term string `arg:"" help:"Search string"`
}
type (
	DBSizeCmd     struct{}
	DBTablesCmd   struct{}
	DBPrefixCmd   struct{}
	DBCheckCmd    struct{}
	DBRepairCmd   struct{}
	DBOptimizeCmd struct{}
	DBResetCmd    struct{}
)

func (c *DBExportCmd) Run(g *Globals) error {
	rc, err := NewRunContext(g)
	if err != nil {
		return err
	}
	defer rc.Close()
	site, err := rc.ResolveSite()
	if err != nil {
		return err
	}

	if c.File == "-" {
		return exportToStdout(rc, site)
	}

	path := c.File
	if path == "" {
		if path, err = defaultDumpPath(site.Alias, "export", time.Now()); err != nil {
			return err
		}
	}
	dump, err := exportToLocal(rc, site, path)
	if err != nil {
		return err
	}
	return printDump(rc, "Exported to:", dump)
}

func (c *DBImportCmd) Run(g *Globals) error {
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
		wpcli.New("db", "import").Arg(c.File).Build(site.WPPath))
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("wp db import: %s", result.Stderr)
	}
	// DB import can change everything — invalidate all categories.
	rc.CacheInvalidate(site.Alias, []string{
		cache.CategoryPlugins, cache.CategoryThemes, cache.CategoryCore,
		cache.CategoryUsers, cache.CategoryOptions, cache.CategorySnapshot,
	})
	fmt.Fprint(rc.Stdout, result.Stdout)
	return nil
}

func (c *DBQueryCmd) Run(g *Globals) error {
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
		wpcli.New("db", "query").Arg(c.SQL).Build(site.WPPath))
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("wp db query: %s", result.Stderr)
	}
	fmt.Fprint(rc.Stdout, result.Stdout)
	return nil
}

func (c *DBSearchCmd) Run(g *Globals) error {
	return execPassthrough(g, "db", "search", c.Term)
}

func (c *DBSizeCmd) Run(g *Globals) error {
	return execPassthrough(g, "db", "size")
}

func (c *DBTablesCmd) Run(g *Globals) error {
	return execPassthrough(g, "db", "tables")
}

func (c *DBPrefixCmd) Run(g *Globals) error {
	return execPassthrough(g, "db", "prefix")
}

func (c *DBCheckCmd) Run(g *Globals) error {
	return execPassthrough(g, "db", "check")
}

func (c *DBRepairCmd) Run(g *Globals) error {
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
		wpcli.New("db", "repair").Build(site.WPPath))
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("wp db repair: %s", result.Stderr)
	}
	rc.CacheInvalidate(site.Alias, []string{
		cache.CategoryPlugins, cache.CategoryThemes, cache.CategoryCore,
		cache.CategoryUsers, cache.CategoryOptions, cache.CategorySnapshot,
	})
	fmt.Fprint(rc.Stdout, result.Stdout)
	return nil
}

func (c *DBOptimizeCmd) Run(g *Globals) error {
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
		wpcli.New("db", "optimize").Build(site.WPPath))
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("wp db optimize: %s", result.Stderr)
	}
	rc.CacheInvalidate(site.Alias, []string{
		cache.CategoryPlugins, cache.CategoryThemes, cache.CategoryCore,
		cache.CategoryUsers, cache.CategoryOptions, cache.CategorySnapshot,
	})
	fmt.Fprint(rc.Stdout, result.Stdout)
	return nil
}

func (c *DBResetCmd) Run(g *Globals) error {
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
		wpcli.New("db", "reset").Build(site.WPPath))
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("wp db reset: %s", result.Stderr)
	}
	rc.CacheInvalidate(site.Alias, []string{
		cache.CategoryPlugins, cache.CategoryThemes, cache.CategoryCore,
		cache.CategoryUsers, cache.CategoryOptions, cache.CategorySnapshot,
	})
	fmt.Fprint(rc.Stdout, result.Stdout)
	return nil
}
