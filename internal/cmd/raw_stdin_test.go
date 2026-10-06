package cmd

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
)

// remoteExec is one exec request the fake SSH server received.
type remoteExec struct {
	command, stdin string
}

// sshSite registers one site of hostType in a temp HOME whose SSH config
// points at an in-process SSH server, and returns a func listing the exec
// requests that server has received. The server reads each command's stdin to
// EOF and answers like wp: "Success" and exit 0.
func sshSite(t *testing.T, hostType string) (execs func() []remoteExec) {
	t.Helper()
	_, hostKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hostSigner, err := ssh.NewSignerFromKey(hostKey)
	if err != nil {
		t.Fatal(err)
	}
	serverCfg := &ssh.ServerConfig{
		PublicKeyCallback: func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) { return &ssh.Permissions{}, nil },
	}
	serverCfg.AddHostKey(hostSigner)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })

	var mu sync.Mutex
	var got []remoteExec
	go func() {
		for {
			nc, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer nc.Close()
				_, chans, reqs, err := ssh.NewServerConn(nc, serverCfg)
				if err != nil {
					return
				}
				go ssh.DiscardRequests(reqs)
				for newCh := range chans {
					ch, chReqs, err := newCh.Accept()
					if err != nil {
						continue
					}
					go func() {
						defer ch.Close()
						for req := range chReqs {
							if req.Type != "exec" {
								_ = req.Reply(false, nil)
								continue
							}
							var payload struct{ Command string }
							_ = ssh.Unmarshal(req.Payload, &payload)
							_ = req.Reply(true, nil)
							in, _ := io.ReadAll(ch)
							mu.Lock()
							got = append(got, remoteExec{payload.Command, string(in)})
							mu.Unlock()
							_, _ = ch.Write([]byte("Success: Updated post 7.\n"))
							_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
							return
						}
					}()
				}
			}()
		}
	}()

	fakeSite(t, hostType)
	home := os.Getenv("HOME")
	_, clientKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(clientKey, "")
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(home, ".ssh", "id_test")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	port := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	sshConfig := "Host site\n  HostName 127.0.0.1\n  Port " + port + "\n  User u\n  IdentityFile " + keyPath + "\n"
	if err := os.WriteFile(filepath.Join(home, ".ssh", "config"), []byte(sshConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSH_AUTH_SOCK", "")

	return func() []remoteExec {
		mu.Lock()
		defer mu.Unlock()
		return append([]remoteExec(nil), got...)
	}
}

// pipeWith returns the read end of an OS pipe holding content, closed for
// writing, as a shell gives a command for `printf content | wpgo raw ...`.
func pipeWith(t *testing.T, content string) *os.File {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	if _, err := w.WriteString(content); err != nil {
		t.Fatal(err)
	}
	w.Close()
	return r
}

// TestRawForwardsPipedStdin runs raw with piped stdin against an SSH server
// and checks the remote wp command receives it only when a "-" argument
// makes wp read it.
func TestRawForwardsPipedStdin(t *testing.T) {
	const body = "<!-- wp:paragraph -->\n<p>Updated body.</p>\n<!-- /wp:paragraph -->\n"
	tests := []struct {
		name      string
		args      []string
		wantStdin string
	}{
		{"post update dash", []string{"post", "update", "7", "-"}, body},
		{"quoted dash", []string{"post", "update", "7", "'-'"}, body},
		{"no dash", []string{"option", "get", "home"}, ""},
	}
	for _, hostType := range []string{"standard", "wpengine"} {
		for _, tt := range tests {
			t.Run(hostType+"/"+tt.name, func(t *testing.T) {
				execs := sshSite(t, hostType)
				err := (&RawCmd{Args: tt.args}).runWithIO(&Globals{Site: "site", NoCache: true}, pipeWith(t, body))
				if err != nil {
					t.Fatalf("raw %q: %v", tt.args, err)
				}
				got := execs()
				if len(got) != 1 || got[0].stdin != tt.wantStdin || !strings.HasSuffix(got[0].command, " && wp "+strings.Join(tt.args, " ")) {
					t.Fatalf("remote got %+v, want one `wp %s` with stdin %q", got, strings.Join(tt.args, " "), tt.wantStdin)
				}
			})
		}
	}
}

// TestRawRefusesStdinReads checks that raw refuses, before anything runs
// remotely, a "-" argument with no input on stdin, including one that only
// WP Engine's second parse turns into "-", and any wp db command that would
// read SQL from stdin. db export's "-", which means stdout, still runs.
func TestRawRefusesStdinReads(t *testing.T) {
	devNull := func(t *testing.T) io.Reader {
		t.Helper()
		f, err := os.Open(os.DevNull)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { f.Close() })
		return f
	}
	piped := func(content string) func(*testing.T) io.Reader {
		return func(t *testing.T) io.Reader {
			t.Helper()
			return pipeWith(t, content)
		}
	}
	const outfile = "SELECT 1 INTO OUTFILE '/tmp/dump.sql';"
	tests := []struct {
		name     string
		hostType string
		args     []string
		stdin    func(*testing.T) io.Reader
		wantErr  string // "" means the command must reach the server
	}{
		{"terminal-like stdin", "standard", []string{"post", "update", "7", "-"}, devNull, "stdin is a terminal"},
		{"empty pipe", "standard", []string{"post", "update", "7", "-"}, piped(""), "empty stdin"},
		{"empty pipe quoted dash", "standard", []string{"post", "update", "7", "'-'"}, piped(""), "empty stdin"},
		{"no stdin", "standard", []string{"post", "update", "7", "-"}, func(*testing.T) io.Reader { return nil }, "stdin is a terminal"},
		{"empty pipe dash after gateway parse", "wpengine", []string{"post", "update", "7", `"'-'"`}, piped(""), "empty stdin"},
		{"piped SQL to db query", "standard", []string{"db", "query"}, piped(outfile), "without SQL"},
		{"piped SQL to db query dash", "standard", []string{"db", "query", "-"}, piped(outfile), "on wp db"},
		{"piped SQL to db import dash", "standard", []string{"sql", "import", "-"}, piped(outfile), "on wp db"},
		{"piped SQL to db import after gateway parse", "wpengine", []string{"db", "'import -'"}, piped(outfile), "on wp db"},
		{"db export to stdout", "standard", []string{"db", "export", "-"}, devNull, ""},
		{"no dash, empty pipe", "standard", []string{"option", "get", "home"}, piped(""), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			execs := sshSite(t, tt.hostType)
			err := (&RawCmd{Args: tt.args}).runWithIO(&Globals{Site: "site", NoCache: true}, tt.stdin(t))
			got := execs()
			if tt.wantErr == "" {
				if err != nil || len(got) != 1 {
					t.Fatalf("raw %q: err = %v, remote got %+v; want it run once", tt.args, err, got)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) || len(got) != 0 {
				t.Fatalf("raw %q: err = %v, remote got %+v; want refusal %q before anything runs", tt.args, err, got, tt.wantErr)
			}
		})
	}
}

// TestRawDryRunLeavesStdin checks that dry-run prints a "-" command without
// reading stdin or refusing for missing input.
func TestRawDryRunLeavesStdin(t *testing.T) {
	args := []string{"post", "update", "7", "-"}
	for _, tt := range []struct {
		name  string
		stdin string
		piped bool
	}{{"piped body", "body", true}, {"no stdin", "", false}} {
		t.Run(tt.name, func(t *testing.T) {
			execs := sshSite(t, "standard")
			var stdin io.Reader
			if tt.piped {
				stdin = pipeWith(t, tt.stdin)
			}
			if err := (&RawCmd{Args: args}).runWithIO(&Globals{Site: "site", NoCache: true, DryRun: true}, stdin); err != nil {
				t.Fatalf("dry-run raw %q: %v", args, err)
			}
			if got := execs(); len(got) != 0 {
				t.Fatalf("dry-run sent %+v", got)
			}
			if stdin == nil {
				return
			}
			left, err := io.ReadAll(stdin)
			if err != nil || string(left) != tt.stdin {
				t.Fatalf("stdin left = %q, %v; want %q unread", left, err, tt.stdin)
			}
		})
	}
}
