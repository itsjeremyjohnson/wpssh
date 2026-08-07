package adapter

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/builtbyrobben/wpssh/internal/registry"
	internalssh "github.com/builtbyrobben/wpssh/internal/ssh"
)

// WPEngineAdapter handles WP Engine's ephemeral SSH environment.
//
// Key differences from standard:
//   - Ephemeral sessions with a 10-minute timeout
//   - No SCP support — file transfers use stdin/stdout piping
//   - WordPress path is ~/sites/{install_name}/
//   - Upload destination: ~/sites/{install_name}/_wpeprivate/
//   - Base64+heredoc for content with special characters
type WPEngineAdapter struct{}

var _ Adapter = (*WPEngineAdapter)(nil)

func (a *WPEngineAdapter) Name() string { return "wpengine" }

func (a *WPEngineAdapter) Capabilities() AdapterCapabilities {
	return AdapterCapabilities{
		SupportsSCP:        false,
		PersistentFS:       false,
		MaxSessionDuration: 10 * time.Minute,
	}
}

// Exec runs a pre-built remote shell command on WP Engine.
// Callers (via wpcli.Command.Build) already include the cd + wp prefix.
// WP Engine's SSH environment places sites at ~/sites/{user}/ when configured.
func (a *WPEngineAdapter) Exec(ctx context.Context, client *internalssh.SSHClient, site *registry.Site, wpCmd string) (internalssh.ExecResult, error) {
	cfg := wpengineClientConfig(site)

	// Enforce WP Engine's 10-minute session timeout.
	timeout := 10 * time.Minute
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining < timeout {
			timeout = remaining
		}
	}
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	return client.Exec(execCtx, cfg, site.CanonicalHost, wpCmd)
}

// Upload transfers a local file to WP Engine via stdin piping.
// Uses base64 encoding to safely transfer binary content.
// Uploads to ~/sites/{user}/_wpeprivate/{filename} by default.
func (a *WPEngineAdapter) Upload(ctx context.Context, client *internalssh.SSHClient, site *registry.Site, localPath, remotePath string) error {
	data, err := os.ReadFile(localPath)
	if err != nil {
		return fmt.Errorf("read local file: %w", err)
	}

	cfg := wpengineClientConfig(site)

	// Use base64 encoding to safely transfer content with special characters.
	encoded := base64.StdEncoding.EncodeToString(data)

	// Pipe base64 through stdin and decode on the remote side.
	cmd := fmt.Sprintf("base64 -d > %s", shellQuote(remotePath))
	result, err := client.ExecWithStdin(ctx, cfg, site.CanonicalHost, cmd, strings.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("upload to wpengine: %w", err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("upload failed (exit %d): %s", result.ExitCode, result.Stderr)
	}
	return nil
}

// Download streams a remote file from WP Engine to local disk via stdout.
func (a *WPEngineAdapter) Download(ctx context.Context, client *internalssh.SSHClient, site *registry.Site, remotePath, localPath string) error {
	cfg := wpengineClientConfig(site)
	cmd := fmt.Sprintf("cat %s", shellQuote(remotePath))
	result, err := client.Exec(ctx, cfg, site.CanonicalHost, cmd)
	if err != nil {
		return fmt.Errorf("download from wpengine: %w", err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("download failed (exit %d): %s", result.ExitCode, result.Stderr)
	}
	if err := os.WriteFile(localPath, []byte(result.Stdout), 0o644); err != nil {
		return fmt.Errorf("write local file: %w", err)
	}
	return nil
}

func wpengineClientConfig(site *registry.Site) internalssh.ClientConfig {
	return internalssh.ClientConfig{
		Host:           site.Hostname,
		Port:           site.Port,
		User:           site.User,
		IdentityFile:   site.IdentityFile,
		ConnectTimeout: 30 * time.Second,
	}
}
