package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/builtbyrobben/wpssh/internal/adapter"
	"github.com/builtbyrobben/wpssh/internal/config"
	"github.com/builtbyrobben/wpssh/internal/registry"
	internalssh "github.com/builtbyrobben/wpssh/internal/ssh"
	"github.com/builtbyrobben/wpssh/internal/wpcli"
)

var (
	errSiteRequired = errors.New("site is required")
	errWPCLIExit    = errors.New("WP-CLI command failed")
)

// Site is the public, non-secret part of a registry entry.
type Site struct {
	Alias    string   `json:"alias"`
	HostType string   `json:"host_type"`
	Groups   []string `json:"groups"`
}

// Service owns site resolution and remote WP-CLI execution for MCP tools.
type Service interface {
	ListSites() []Site
	Run(context.Context, string, *wpcli.Command) (string, error)
	Close() error
}

type liveService struct {
	registry *registry.Registry
	ssh      *internalssh.SSHClient
}

// NewService loads the server's site registry and starts its SSH pool.
func NewService() (Service, error) {
	paths := config.DefaultPaths()

	cfg, err := config.Load(paths.ConfigFile())
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	reg, err := registry.NewRegistry(registry.RegistryOptions{UserGroups: cfg.UserGroups()})
	if err != nil {
		return nil, fmt.Errorf("load site registry: %w", err)
	}

	hostConfigs := make(map[string]internalssh.HostConfig, len(cfg.RateLimits))
	for host, rl := range cfg.RateLimits {
		hostConfigs[host] = internalssh.HostConfig{Delay: rl.Delay, MaxConns: rl.MaxConns}
	}
	pool := internalssh.NewVerifiedPool(internalssh.NewRateLimiter(hostConfigs), 5*time.Minute)

	return &liveService{registry: reg, ssh: internalssh.NewSSHClient(pool)}, nil
}

func (s *liveService) ListSites() []Site {
	sites := s.registry.List()

	out := make([]Site, 0, len(sites))
	for _, site := range sites {
		out = append(out, Site{Alias: site.Alias, HostType: site.HostType, Groups: site.Groups})
	}

	return out
}

func (s *liveService) Run(ctx context.Context, alias string, command *wpcli.Command) (string, error) {
	if alias == "" {
		return "", errSiteRequired
	}

	site, err := s.registry.Get(alias)
	if err != nil {
		return "", fmt.Errorf("resolve site: %w", err)
	}

	result, err := adapter.ForSite(site).Exec(ctx, s.ssh, site, command.Build(site.WPPath))
	if err != nil {
		return "", fmt.Errorf("execute over SSH: %w", err)
	}

	if result.ExitCode != 0 {
		return "", fmt.Errorf("%w (exit %d): %s", errWPCLIExit, result.ExitCode, strings.TrimSpace(result.Stderr))
	}

	return result.Stdout, nil
}

func (s *liveService) Close() error {
	if err := s.ssh.Close(); err != nil {
		return fmt.Errorf("close SSH pool: %w", err)
	}

	return nil
}
