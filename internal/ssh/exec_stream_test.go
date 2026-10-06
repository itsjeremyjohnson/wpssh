package ssh

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// fakeCommand is what the in-process server sends back for one exec request.
type fakeCommand struct {
	stdout, stderr string
	exit           uint32
}

// newFakeSSHClient returns an SSHClient whose pooled connection for cfg/host
// goes to an in-process SSH server answering exec requests from commands.
func newFakeSSHClient(t *testing.T, commands map[string]fakeCommand) (*SSHClient, ClientConfig, string) {
	t.Helper()
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
	t.Cleanup(func() { listener.Close() })
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
		go ssh.DiscardRequests(reqs)
		for newCh := range chans {
			ch, chReqs, acceptErr := newCh.Accept()
			if acceptErr != nil {
				continue
			}
			go serveExec(ch, chReqs, commands)
		}
	}()

	clientNet, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	clientConn, chans, reqs, err := ssh.NewClientConn(clientNet, "test:22", &ssh.ClientConfig{
		User: "alice", HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	})
	if err != nil {
		t.Fatal(err)
	}
	client := ssh.NewClient(clientConn, chans, reqs)

	const host = "192.0.2.1:22"
	cfg := ClientConfig{Host: "test", Port: 22, User: "alice"}
	pool := NewPool(NewRateLimiter(map[string]HostConfig{host: {MaxConns: 1}}), time.Minute)
	t.Cleanup(func() { pool.Close() })
	pool.connections[connectionKey(cfg, host)] = &poolEntry{client: client, lastUsed: time.Now()}
	return NewSSHClient(pool), cfg, host
}

func serveExec(ch ssh.Channel, reqs <-chan *ssh.Request, commands map[string]fakeCommand) {
	defer ch.Close()
	for req := range reqs {
		if req.Type != "exec" {
			_ = req.Reply(false, nil)
			continue
		}
		var payload struct{ Command string }
		if err := ssh.Unmarshal(req.Payload, &payload); err != nil {
			_ = req.Reply(false, nil)
			return
		}
		_ = req.Reply(true, nil)
		c := commands[payload.Command]
		_, _ = ch.Write([]byte(c.stdout))
		_, _ = ch.Stderr().Write([]byte(c.stderr))
		_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{c.exit}))
		return
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestExecStream(t *testing.T) {
	client, cfg, host := newFakeSSHClient(t, map[string]fakeCommand{
		"dump": {stdout: "-- data --\n", stderr: "Warning: deprecated\n", exit: 0},
		"fail": {stdout: "partial", stderr: "mysqldump: Lost connection\n", exit: 3},
	})
	ctx := context.Background()

	t.Run("stdout to writer, stderr separate", func(t *testing.T) {
		var out bytes.Buffer
		res, err := client.ExecStream(ctx, cfg, host, "dump", &out)
		if err != nil {
			t.Fatal(err)
		}
		if out.String() != "-- data --\n" || res.Stdout != "" || res.Stderr != "Warning: deprecated\n" || res.ExitCode != 0 {
			t.Errorf("writer %q, result %+v", out.String(), res)
		}
	})

	t.Run("remote exit status", func(t *testing.T) {
		var out bytes.Buffer
		res, err := client.ExecStream(ctx, cfg, host, "fail", &out)
		if err != nil {
			t.Fatal(err)
		}
		if res.ExitCode != 3 || res.Stderr != "mysqldump: Lost connection\n" || out.String() != "partial" {
			t.Errorf("writer %q, result %+v", out.String(), res)
		}
	})

	t.Run("writer failure is an error", func(t *testing.T) {
		_, err := client.ExecStream(ctx, cfg, host, "dump", failingWriter{})
		if err == nil || !strings.Contains(err.Error(), "disk full") {
			t.Fatalf("err = %v, want the writer's error", err)
		}
	})
}
