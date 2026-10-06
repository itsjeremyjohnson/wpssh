package adapter

import (
	"context"
	"io"
	"time"

	"github.com/builtbyrobben/wpssh/internal/registry"
	internalssh "github.com/builtbyrobben/wpssh/internal/ssh"
)

// AdapterCapabilities describes what a host adapter supports.
type AdapterCapabilities struct {
	SupportsSCP        bool
	PersistentFS       bool
	MaxSessionDuration time.Duration // 0 means no limit.
}

// Adapter defines how commands and file transfers are executed on a host.
// Different hosting environments (cPanel, WP Engine) implement this interface.
type Adapter interface {
	// Exec runs a wp-cli command on the remote site.
	// The wpCmd should be the wp-cli arguments (e.g., "plugin list --format=json").
	Exec(ctx context.Context, client *internalssh.SSHClient, site *registry.Site, wpCmd string) (internalssh.ExecResult, error)

	// ExecStream runs a wp-cli command like Exec but copies its stdout to
	// stdout as it arrives; the returned ExecResult has an empty Stdout.
	ExecStream(ctx context.Context, client *internalssh.SSHClient, site *registry.Site, wpCmd string, stdout io.Writer) (internalssh.ExecResult, error)

	// ExecStdin runs a wp-cli command like Exec with stdin copied to the
	// command's stdin; the command sees EOF when stdin returns io.EOF or an
	// error.
	ExecStdin(ctx context.Context, client *internalssh.SSHClient, site *registry.Site, wpCmd string, stdin io.Reader) (internalssh.ExecResult, error)

	// Upload transfers a local file to the remote host.
	Upload(ctx context.Context, client *internalssh.SSHClient, site *registry.Site, localPath, remotePath string) error

	// Download transfers a remote file to local disk.
	Download(ctx context.Context, client *internalssh.SSHClient, site *registry.Site, remotePath, localPath string) error

	// Capabilities returns what this adapter supports.
	Capabilities() AdapterCapabilities

	// Name returns the adapter identifier (e.g., "standard", "wpengine").
	Name() string
}
