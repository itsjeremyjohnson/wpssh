package ssh

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestReusedConnectionKeepsHostSlotUntilRelease(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	serverCfg := &ssh.ServerConfig{NoClientAuth: true}
	serverCfg.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		serverNet, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer serverNet.Close()
		conn, chans, reqs, serverErr := ssh.NewServerConn(serverNet, serverCfg)
		if serverErr != nil {
			return
		}
		defer conn.Close()
		go func() {
			for channel := range chans {
				_ = channel.Reject(ssh.Prohibited, "unused")
			}
		}()
		for req := range reqs {
			_ = req.Reply(true, nil)
		}
	}()
	clientNet, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer clientNet.Close()
	clientConn, chans, reqs, err := ssh.NewClientConn(clientNet, "test:22", &ssh.ClientConfig{
		User: "alice", HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	})
	if err != nil {
		t.Fatal(err)
	}
	client := ssh.NewClient(clientConn, chans, reqs)
	defer client.Close()

	const host = "192.0.2.1:22"
	cfg := ClientConfig{Host: "test", Port: 22, User: "alice"}
	pool := NewPool(NewRateLimiter(map[string]HostConfig{host: {Delay: 0, MaxConns: 1}}), time.Minute)
	defer pool.Close()
	pool.connections[connectionKey(cfg, host)] = &poolEntry{client: client, lastUsed: time.Now()}

	_, release, err := pool.Get(context.Background(), cfg, host)
	if err != nil {
		t.Fatal(err)
	}
	entry := pool.connections[connectionKey(cfg, host)]
	entry.mu.Lock()
	entry.lastUsed = time.Now().Add(-2 * time.Minute)
	entry.mu.Unlock()
	pool.reapIdle()
	if _, ok := pool.connections[connectionKey(cfg, host)]; !ok {
		t.Fatal("idle reaper closed an active connection")
	}
	done := make(chan error, 1)
	go func() {
		_, releaseSecond, getErr := pool.Get(context.Background(), cfg, host)
		if getErr == nil {
			releaseSecond()
		}
		done <- getErr
	}()
	select {
	case err := <-done:
		t.Fatalf("second operation bypassed host slot: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("second operation failed after release: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("second operation did not resume after release")
	}
	entry.mu.Lock()
	entry.lastUsed = time.Now().Add(-2 * time.Minute)
	entry.mu.Unlock()
	pool.reapIdle()
	if _, ok := pool.connections[connectionKey(cfg, host)]; ok {
		t.Fatal("idle reaper retained an unused expired connection")
	}
}

func TestGetCancelsWhileIdentityIsLocked(t *testing.T) {
	const host = "192.0.2.1:22"
	cfg := ClientConfig{Host: "test", Port: 22, User: "alice"}
	pool := NewPool(NewRateLimiter(map[string]HostConfig{host: {MaxConns: 2}}), time.Minute)
	defer pool.Close()
	keyLock := make(chan struct{}, 1)
	keyLock <- struct{}{}
	pool.keyLocks.Store(connectionKey(cfg, host), keyLock)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _, err := pool.Get(ctx, cfg, host)
	if err != context.DeadlineExceeded {
		t.Fatalf("Get() = %v, want deadline exceeded", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("Get did not return promptly after cancellation")
	}
}
