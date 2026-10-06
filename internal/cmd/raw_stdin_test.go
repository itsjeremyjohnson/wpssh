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
// and checks the remote wp command receives it.
func TestRawForwardsPipedStdin(t *testing.T) {
	const body = "<!-- wp:paragraph -->\n<p>Updated body.</p>\n<!-- /wp:paragraph -->\n"
	tests := []struct {
		name string
		args []string
	}{
		{"post update dash", []string{"post", "update", "7", "-"}},
		{"quoted dash", []string{"post", "update", "7", "'-'"}},
		{"stdin without dash", []string{"db", "query"}},
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
				if len(got) != 1 || got[0].stdin != body || !strings.HasSuffix(got[0].command, " && wp "+strings.Join(tt.args, " ")) {
					t.Fatalf("remote got %+v, want one `wp %s` with stdin %q", got, strings.Join(tt.args, " "), body)
				}
			})
		}
	}
}

// TestRawRefusesDashWithoutInput checks that a "-" argument with no input on
// stdin is refused before anything runs remotely, while db export's "-",
// which means stdout, still runs.
func TestRawRefusesDashWithoutInput(t *testing.T) {
	devNull := func(t *testing.T) io.Reader {
		t.Helper()
		f, err := os.Open(os.DevNull)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { f.Close() })
		return f
	}
	empty := func(t *testing.T) io.Reader {
		t.Helper()
		return pipeWith(t, "")
	}
	tests := []struct {
		name    string
		args    []string
		stdin   func(*testing.T) io.Reader
		wantErr string // "" means the command must reach the server
	}{
		{"terminal-like stdin", []string{"post", "update", "7", "-"}, devNull, "stdin is a terminal"},
		{"empty pipe", []string{"post", "update", "7", "-"}, empty, "empty stdin"},
		{"empty pipe quoted dash", []string{"post", "update", "7", "'-'"}, empty, "empty stdin"},
		{"no stdin", []string{"post", "update", "7", "-"}, func(*testing.T) io.Reader { return nil }, "stdin is a terminal"},
		{"db export to stdout", []string{"db", "export", "-"}, devNull, ""},
		{"no dash, empty pipe", []string{"option", "get", "home"}, empty, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			execs := sshSite(t, "standard")
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
