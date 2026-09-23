package ssh

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// ClientConfig holds the SSH connection parameters for a host.
type ClientConfig struct {
	Host           string
	Port           int
	User           string
	IdentityFile   string
	Passphrase     string
	ConnectTimeout time.Duration
	ForwardAgent   bool
}

// ExecResult captures the output and status of a remote command execution.
type ExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Duration time.Duration
}

// SSHClient executes commands on remote hosts via SSH, using a connection
// pool and per-host rate limiter.
type SSHClient struct {
	pool *Pool
}

// NewSSHClient creates a client backed by the given connection pool.
func NewSSHClient(pool *Pool) *SSHClient {
	return &SSHClient{pool: pool}
}

// Exec runs a command on the specified host and returns the result.
// The canonicalHost parameter is the resolved IP:port used for rate limiting
// and connection pooling.
func (c *SSHClient) Exec(ctx context.Context, cfg ClientConfig, canonicalHost, command string) (ExecResult, error) {
	return c.execInternal(ctx, cfg, canonicalHost, command, nil)
}

// ExecWithStdin runs a command on the specified host, piping stdin data.
// Used for WP Engine adapter (stdin piping for file transfers).
func (c *SSHClient) ExecWithStdin(ctx context.Context, cfg ClientConfig, canonicalHost, command string, stdin io.Reader) (ExecResult, error) {
	return c.execInternal(ctx, cfg, canonicalHost, command, stdin)
}

func (c *SSHClient) execInternal(ctx context.Context, cfg ClientConfig, canonicalHost, command string, stdin io.Reader) (ExecResult, error) {
	start := time.Now()

	client, release, err := c.pool.Get(ctx, cfg, canonicalHost)
	if err != nil {
		return ExecResult{Duration: time.Since(start)}, fmt.Errorf("get connection: %w", err)
	}
	defer release()

	// Set up agent forwarding if requested.
	if cfg.ForwardAgent {
		agentConn, err := setupAgentForwarding(client)
		if err != nil {
			// Non-fatal: log but continue without forwarding.
			_ = err
		} else if agentConn != nil {
			defer agentConn.Close()
		}
	}

	session, err := client.NewSession()
	if err != nil {
		// Session creation failure is a transport error — connection may be dead.
		c.pool.Remove(cfg, canonicalHost)
		return ExecResult{Duration: time.Since(start)}, fmt.Errorf("new session: %w", err)
	}
	defer session.Close()

	// Request agent forwarding on the session if applicable.
	if cfg.ForwardAgent {
		_ = agent.RequestAgentForwarding(session)
	}

	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	if stdin != nil {
		session.Stdin = stdin
	}

	// Run with context cancellation.
	done := make(chan error, 1)
	go func() {
		done <- session.Run(command)
	}()

	select {
	case err = <-done:
	case <-ctx.Done():
		session.Signal(ssh.SIGTERM)
		session.Close()
		return ExecResult{Duration: time.Since(start)}, ctx.Err()
	}

	result := ExecResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: 0,
		Duration: time.Since(start),
	}

	if err != nil {
		if exitErr, ok := err.(*ssh.ExitError); ok {
			result.ExitCode = exitErr.ExitStatus()
		} else {
			// Non-exit error is a transport error.
			return result, fmt.Errorf("run command: %w", err)
		}
	}

	return result, nil
}

// Close shuts down the underlying connection pool.
func (c *SSHClient) Close() error {
	return c.pool.Close()
}
