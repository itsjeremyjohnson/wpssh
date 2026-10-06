package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/builtbyrobben/wpssh/internal/adapter"
	"github.com/builtbyrobben/wpssh/internal/cache"
	"github.com/builtbyrobben/wpssh/internal/config"
	"github.com/builtbyrobben/wpssh/internal/outfmt"
	"github.com/builtbyrobben/wpssh/internal/registry"
	internalssh "github.com/builtbyrobben/wpssh/internal/ssh"
)

// RunContext wires together all the services needed to execute a wp-cli command.
type RunContext struct {
	Registry  *registry.Registry
	Config    *config.Config
	SSHClient *internalssh.SSHClient
	Cache     *cache.Cache
	Formatter *outfmt.Formatter
	Globals   *Globals
	Stdout    io.Writer
	Stderr    io.Writer
}

// NewRunContext builds a fully wired RunContext from Globals.
// The caller must call Close() when done to release SSH connections and cache.
func NewRunContext(g *Globals) (*RunContext, error) {
	paths := config.DefaultPaths()
	cfg, err := config.Load(paths.ConfigFile())
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	reg, err := registry.NewRegistry(registry.RegistryOptions{
		UserGroups: cfg.UserGroups(),
	})
	if err != nil {
		return nil, fmt.Errorf("build registry: %w", err)
	}

	// Build rate limiter with config.
	hostConfigs := make(map[string]internalssh.HostConfig)
	for host, rl := range cfg.RateLimits {
		hostConfigs[host] = internalssh.HostConfig{
			Delay:    rl.Delay,
			MaxConns: rl.MaxConns,
		}
	}
	limiter := internalssh.NewRateLimiter(hostConfigs)
	pool := internalssh.NewPool(limiter, 5*time.Minute)
	sshClient := internalssh.NewSSHClient(pool)

	var cacheStore *cache.Cache
	if !g.NoCache {
		cacheStore, err = cache.New(paths.CacheDB())
		if err != nil {
			// Non-fatal: proceed without cache.
			fmt.Fprintf(os.Stderr, "warning: cache unavailable: %v\n", err)
		}
	}

	formatter := outfmt.New(g.JSON, g.Plain, g.Fields)

	return &RunContext{
		Registry:  reg,
		Config:    cfg,
		SSHClient: sshClient,
		Cache:     cacheStore,
		Formatter: formatter,
		Globals:   g,
		Stdout:    os.Stdout,
		Stderr:    os.Stderr,
	}, nil
}

// Close releases resources held by the RunContext.
func (rc *RunContext) Close() {
	if rc.SSHClient != nil {
		rc.SSHClient.Close()
	}
	if rc.Cache != nil {
		rc.Cache.Close()
	}
}

// ResolveSite resolves the target site from --site flag or config default.
func (rc *RunContext) ResolveSite() (*registry.Site, error) {
	alias := rc.Globals.Site
	if alias == "" {
		alias = rc.Config.DefaultSite
	}
	if alias == "" {
		return nil, fmt.Errorf("no site specified: use --site or set default_site in config")
	}
	return rc.Registry.Get(alias)
}

// ExecWP executes a wp-cli command on a site and returns the result.
func (rc *RunContext) ExecWP(ctx context.Context, site *registry.Site, wpCmd string) (internalssh.ExecResult, error) {
	a := adapter.ForSite(site)
	if rc.Globals.Verbose {
		fmt.Fprintf(rc.Stderr, "[adapter: %s] %s\n", a.Name(), wpCmd)
	}
	if rc.Globals.DryRun {
		fmt.Fprintf(rc.Stderr, "[dry-run] %s\n", wpCmd)
		return internalssh.ExecResult{}, nil
	}
	return a.Exec(ctx, rc.SSHClient, site, wpCmd)
}

// ExecWPStream executes a wp-cli command on a site and copies its stdout to
// stdout as it arrives. In dry-run mode it prints the command and writes
// nothing.
func (rc *RunContext) ExecWPStream(ctx context.Context, site *registry.Site, wpCmd string, stdout io.Writer) (internalssh.ExecResult, error) {
	a := adapter.ForSite(site)
	if rc.Globals.Verbose {
		fmt.Fprintf(rc.Stderr, "[adapter: %s] %s\n", a.Name(), wpCmd)
	}
	if rc.Globals.DryRun {
		fmt.Fprintf(rc.Stderr, "[dry-run] %s\n", wpCmd)
		return internalssh.ExecResult{}, nil
	}
	return a.ExecStream(ctx, rc.SSHClient, site, wpCmd, stdout)
}

// ExecWPWithStdin executes a wp-cli command on a site with stdin as its
// standard input. In dry-run mode it prints the command and reads nothing.
func (rc *RunContext) ExecWPWithStdin(ctx context.Context, site *registry.Site, wpCmd string, stdin io.Reader) (internalssh.ExecResult, error) {
	a := adapter.ForSite(site)
	if rc.Globals.Verbose {
		fmt.Fprintf(rc.Stderr, "[adapter: %s] %s\n", a.Name(), wpCmd)
	}
	if rc.Globals.DryRun {
		fmt.Fprintf(rc.Stderr, "[dry-run] %s\n", wpCmd)
		return internalssh.ExecResult{}, nil
	}
	return a.ExecWithStdin(ctx, rc.SSHClient, site, wpCmd, stdin)
}

// CacheGet checks the cache for a stored result. Returns the cached data
// string or empty string on miss. Handles nil cache gracefully.
func (rc *RunContext) CacheGet(siteAlias, commandCacheKey string) string {
	if rc.Cache == nil {
		return ""
	}
	key := cache.MakeCacheKey(siteAlias, commandCacheKey)
	entry, err := rc.Cache.Get(siteAlias, key)
	if err != nil || entry == nil {
		return ""
	}
	// Check TTL expiry.
	if time.Since(entry.FetchedAt) > time.Duration(entry.TTLSeconds)*time.Second {
		return ""
	}
	if rc.Globals.Verbose {
		fmt.Fprintf(rc.Stderr, "[cache hit] %s\n", entry.Command)
	}
	return entry.Data
}

// CacheSet stores a result in the cache. Handles nil cache gracefully.
func (rc *RunContext) CacheSet(siteAlias, commandCacheKey, command, data, category string, ttlSeconds int) {
	if rc.Cache == nil {
		return
	}
	key := cache.MakeCacheKey(siteAlias, commandCacheKey)
	entry := &cache.CacheEntry{
		SiteAlias:  siteAlias,
		CacheKey:   key,
		Command:    command,
		Data:       data,
		FetchedAt:  time.Now(),
		TTLSeconds: ttlSeconds,
		Category:   category,
	}
	if err := rc.Cache.Set(entry); err != nil && rc.Globals.Verbose {
		fmt.Fprintf(rc.Stderr, "[cache] store error: %v\n", err)
	}
}

// CacheInvalidate invalidates cache entries for a site. Handles nil cache.
func (rc *RunContext) CacheInvalidate(siteAlias string, categories []string) {
	if rc.Cache == nil {
		return
	}
	if err := rc.Cache.Invalidate(siteAlias, categories); err != nil && rc.Globals.Verbose {
		fmt.Fprintf(rc.Stderr, "[cache] invalidate error: %v\n", err)
	}
}

// SSHConfig builds an SSH ClientConfig from a registry Site.
func SSHConfig(site *registry.Site) internalssh.ClientConfig {
	return internalssh.ClientConfig{
		Host:           site.Hostname,
		Port:           site.Port,
		User:           site.User,
		IdentityFile:   site.IdentityFile,
		ConnectTimeout: 30 * time.Second,
	}
}
