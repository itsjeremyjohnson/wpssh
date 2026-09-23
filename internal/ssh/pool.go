package ssh

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// poolEntry holds a cached SSH connection and its metadata.
type poolEntry struct {
	client   *ssh.Client
	lastUsed time.Time
	active   int
	mu       sync.Mutex // Guards concurrent session creation on same connection.
}

// Pool manages reusable SSH connections and limits each command per host.
type Pool struct {
	mu             sync.Mutex
	connections    map[string]*poolEntry // Keyed by destination and SSH identity.
	keyLocks       sync.Map              // Channels serialize connection setup per SSH identity.
	limiter        *RateLimiter
	idleTimeout    time.Duration
	verifyHostKeys bool
	closed         bool
}

// NewPool creates a connection pool that integrates with the given rate limiter.
func NewPool(limiter *RateLimiter, idleTimeout time.Duration) *Pool {
	return newPool(limiter, idleTimeout, false)
}

// NewVerifiedPool requires a pinned SSH host key for every connection.
func NewVerifiedPool(limiter *RateLimiter, idleTimeout time.Duration) *Pool {
	return newPool(limiter, idleTimeout, true)
}

func newPool(limiter *RateLimiter, idleTimeout time.Duration, verifyHostKeys bool) *Pool {
	if idleTimeout == 0 {
		idleTimeout = 5 * time.Minute
	}
	p := &Pool{
		connections:    make(map[string]*poolEntry),
		limiter:        limiter,
		idleTimeout:    idleTimeout,
		verifyHostKeys: verifyHostKeys,
	}
	go p.reapLoop()
	return p
}

// Get returns an SSH client for the given config. If a healthy cached
// connection exists, it is reused. Otherwise, a new connection is dialed.
// Every call holds a host rate-limiter slot until release.
//
// The returned release function MUST be called when the caller is done
// using the connection. It updates last-used time (the connection stays
// in the pool for reuse).
func (p *Pool) Get(ctx context.Context, cfg ClientConfig, canonicalHost string) (*ssh.Client, func(), error) {
	key := connectionKey(cfg, canonicalHost)
	slotRelease, err := p.limiter.Acquire(ctx, canonicalHost)
	if err != nil {
		return nil, nil, fmt.Errorf("rate limiter: %w", err)
	}
	keyLock, _ := p.keyLocks.LoadOrStore(key, make(chan struct{}, 1))
	select {
	case keyLock.(chan struct{}) <- struct{}{}:
		defer func() { <-keyLock.(chan struct{}) }()
	case <-ctx.Done():
		slotRelease()
		return nil, nil, ctx.Err()
	}

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		slotRelease()
		return nil, nil, fmt.Errorf("pool is closed")
	}

	// Check for existing healthy connection.
	if entry, ok := p.connections[key]; ok {
		entry.mu.Lock()
		entry.active++
		shared := entry.active > 1
		entry.mu.Unlock()
		p.mu.Unlock()
		// A live session already proves a shared connection is usable. Probing
		// it could close that session when this request is cancelled.
		var err error
		if !shared {
			err = keepalive(ctx, entry.client)
		}
		if err == nil {
			entry.mu.Lock()
			entry.lastUsed = time.Now()
			entry.mu.Unlock()
			release := func() {
				entry.mu.Lock()
				entry.active--
				entry.lastUsed = time.Now()
				entry.mu.Unlock()
				slotRelease()
			}
			return entry.client, release, nil
		}
		// Connection is dead; remove and dial fresh.
		p.mu.Lock()
		delete(p.connections, key)
		entry.mu.Lock()
		entry.active--
		entry.mu.Unlock()
		entry.client.Close()
		p.mu.Unlock()
		if ctx.Err() != nil {
			slotRelease()
			return nil, nil, ctx.Err()
		}
	} else {
		p.mu.Unlock()
	}

	client, err := dial(ctx, cfg, p.verifyHostKeys)
	if err != nil {
		slotRelease()
		if ctx.Err() == nil {
			p.limiter.OnTransportError(canonicalHost)
		}
		return nil, nil, err
	}

	p.limiter.OnSuccess(canonicalHost)

	entry := &poolEntry{
		client:   client,
		lastUsed: time.Now(),
		active:   1,
	}

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		client.Close()
		slotRelease()
		return nil, nil, fmt.Errorf("pool is closed")
	}
	p.connections[key] = entry
	p.mu.Unlock()

	release := func() {
		entry.mu.Lock()
		entry.active--
		entry.lastUsed = time.Now()
		entry.mu.Unlock()
		slotRelease()
	}
	return client, release, nil
}

// Close shuts down all pooled connections and prevents new ones.
func (p *Pool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	for host, entry := range p.connections {
		entry.client.Close()
		delete(p.connections, host)
	}
	return nil
}

// Remove closes and removes a specific host's connection from the pool.
func (p *Pool) Remove(cfg ClientConfig, canonicalHost string) {
	key := connectionKey(cfg, canonicalHost)
	keyLock, _ := p.keyLocks.LoadOrStore(key, make(chan struct{}, 1))
	keyLock.(chan struct{}) <- struct{}{}
	defer func() { <-keyLock.(chan struct{}) }()

	p.mu.Lock()
	defer p.mu.Unlock()
	if entry, ok := p.connections[key]; ok {
		entry.mu.Lock()
		active := entry.active
		entry.mu.Unlock()
		if active > 1 {
			return
		}
		entry.client.Close()
		delete(p.connections, key)
	}
}

func keepalive(ctx context.Context, client *ssh.Client) error {
	done := make(chan error, 1)
	go func() {
		_, _, err := client.SendRequest("keepalive@openssh.com", true, nil)
		done <- err
	}()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		_ = client.Close()
		return ctx.Err()
	case <-timer.C:
		_ = client.Close()
		return errors.New("SSH keepalive timed out")
	}
}

func connectionKey(cfg ClientConfig, canonicalHost string) string {
	passphraseHash := sha256.Sum256([]byte(cfg.Passphrase))
	return fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%d\x00%t\x00%x", canonicalHost, cfg.Host, cfg.User, cfg.IdentityFile, cfg.Port, cfg.ForwardAgent, passphraseHash)
}

// reapLoop periodically closes idle connections.
func (p *Pool) reapLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		if p.reapIdle() {
			return
		}
	}
}

func (p *Pool) reapIdle() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return true
	}
	now := time.Now()
	for key, entry := range p.connections {
		entry.mu.Lock()
		idle := now.Sub(entry.lastUsed)
		active := entry.active
		entry.mu.Unlock()
		if active == 0 && idle > p.idleTimeout {
			entry.client.Close()
			delete(p.connections, key)
		}
	}
	return false
}

// dial creates a new SSH connection using the provided config.
func dial(ctx context.Context, cfg ClientConfig, verifyHostKeys bool) (*ssh.Client, error) {
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)

	// Build auth methods.
	var authMethods []ssh.AuthMethod

	// Try identity file first.
	if cfg.IdentityFile != "" {
		var signer ssh.Signer
		var err error
		if cfg.Passphrase != "" {
			signer, err = LoadKeyWithPassphrase(cfg.IdentityFile, cfg.Passphrase)
		} else {
			signer, err = LoadKey(cfg.IdentityFile)
		}
		if err == nil {
			authMethods = append(authMethods, ssh.PublicKeys(signer))
		}
	}

	// Try SSH agent.
	signers, err := AgentSigners()
	if err == nil && len(signers) > 0 {
		authMethods = append(authMethods, ssh.PublicKeys(signers...))
	}

	if len(authMethods) == 0 {
		return nil, fmt.Errorf("no auth methods available for %s", addr)
	}

	hostKeyCallback := ssh.InsecureIgnoreHostKey() // #nosec G106 -- preserves the legacy CLI's existing behavior.
	if verifyHostKeys {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("locate SSH known_hosts: %w", err)
		}
		hostKeyCallback, err = loadHostKeyCallback(filepath.Join(home, ".ssh", "known_hosts"))
		if err != nil {
			return nil, fmt.Errorf("load SSH known_hosts: %w", err)
		}
	}
	sshCfg := &ssh.ClientConfig{
		User:            cfg.User,
		Auth:            authMethods,
		HostKeyCallback: hostKeyCallback,
		Timeout:         cfg.ConnectTimeout,
	}

	// Dial with context for cancellation support.
	dialer := net.Dialer{Timeout: cfg.ConnectTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}

	sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, sshCfg)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("ssh handshake %s: %w", addr, err)
	}

	return ssh.NewClient(sshConn, chans, reqs), nil
}

func loadHostKeyCallback(path string) (ssh.HostKeyCallback, error) {
	return knownhosts.New(path)
}
