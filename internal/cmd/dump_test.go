package cmd

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	internalssh "github.com/builtbyrobben/wpssh/internal/ssh"
	"github.com/builtbyrobben/wpssh/internal/wpcli"
)

const completeDump = "CREATE TABLE wp_posts (ID int);\n-- Dump completed on 2026-10-06 12:00:00\n"

// stubWPScript is a fake wp for `wp db export -`. STUB_MODE picks the outcome;
// any other export target is written as a file so a stray remote write shows up.
const stubWPScript = `#!/bin/sh
[ "$1 $2" = "db export" ] || exit 2
if [ "$3" != "-" ]; then printf 'dump\n' > "$3"; exit 0; fi
case "$STUB_MODE" in
ok) printf '%s' "$STUB_DUMP" ;;
fail) printf 'CREATE TABLE wp_po'; echo 'mysqldump: Lost connection' >&2; exit 3 ;;
empty) ;;
truncated) printf 'CREATE TABLE wp_posts (ID int);\nINSERT INTO wp_po' ;;
esac
`

// remoteShells returns sh plus dash when it is installed (sh is dash on
// Ubuntu CI).
func remoteShells(t *testing.T) []string {
	t.Helper()
	shells := []string{"sh"}
	if _, err := exec.LookPath("dash"); err == nil {
		shells = append(shells, "dash")
	} else {
		t.Log("dash not installed; testing sh only")
	}
	return shells
}

// fakeRemote sets up a temp "server" HOME with a WordPress path and stub wp,
// and returns a run func that executes the real remote export command there.
func fakeRemote(t *testing.T, shell, mode string) (home string, run func(io.Writer) (internalssh.ExecResult, error)) {
	t.Helper()
	home = t.TempDir()
	wpPath := filepath.Join(home, "public_html", "my site")
	if err := os.MkdirAll(wpPath, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "wp"), []byte(stubWPScript), 0o755); err != nil {
		t.Fatal(err)
	}
	return home, func(w io.Writer) (internalssh.ExecResult, error) {
		cmd := exec.Command(shell, "-c", wpcli.DBExport(wpPath, "-", ""))
		cmd.Env = []string{"HOME=" + home, "PATH=" + bin + ":" + os.Getenv("PATH"), "STUB_MODE=" + mode, "STUB_DUMP=" + completeDump}
		var stderr bytes.Buffer
		cmd.Stdout = w
		cmd.Stderr = &stderr
		err := cmd.Run()
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return internalssh.ExecResult{Stderr: stderr.String(), ExitCode: exitErr.ExitCode()}, nil
		}
		return internalssh.ExecResult{Stderr: stderr.String()}, err
	}
}

func TestDefaultDumpPath(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

	t.Run("home default", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("WPGO_LOCAL_BACKUP_DIR", "")
		got, err := defaultDumpPath("my site", "pre update", now)
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(home, "wpgo-backups", "my_site", "my_site_DB_pre_update_20261006_120000.sql")
		if got != want {
			t.Fatalf("path = %s, want %s", got, want)
		}
		assertPerm(t, filepath.Join(home, "wpgo-backups"), 0o700)
		assertPerm(t, filepath.Join(home, "wpgo-backups", "my_site"), 0o700)
	})

	t.Run("env override tightens existing dir", func(t *testing.T) {
		base := filepath.Join(t.TempDir(), "dumps")
		if err := os.MkdirAll(base, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("WPGO_LOCAL_BACKUP_DIR", base)
		got, err := defaultDumpPath("acme", "export", now)
		if err != nil {
			t.Fatal(err)
		}
		if want := filepath.Join(base, "acme", "acme_DB_export_20261006_120000.sql"); got != want {
			t.Fatalf("path = %s, want %s", got, want)
		}
		assertPerm(t, base, 0o700)
	})
}

// TestSaveDumpStreamsToOperator runs the default export command against a
// local stand-in for the server and checks the dump lands only locally.
func TestSaveDumpStreamsToOperator(t *testing.T) {
	sum := sha256.Sum256([]byte(completeDump))
	wantSHA := hex.EncodeToString(sum[:])

	for _, shell := range remoteShells(t) {
		t.Run(shell, func(t *testing.T) {
			serverHome, run := fakeRemote(t, shell, "ok")
			path := filepath.Join(t.TempDir(), "site_DB_export.sql")

			got, err := saveDump(path, run)
			if err != nil {
				t.Fatal(err)
			}
			if got.Path != path || got.Bytes != int64(len(completeDump)) || got.SHA256 != wantSHA {
				t.Errorf("saveDump = %+v, want path %s, %d bytes, sha %s", got, path, len(completeDump), wantSHA)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != completeDump {
				t.Errorf("local dump = %q, want %q", data, completeDump)
			}
			assertPerm(t, path, 0o600)
			if left := listFiles(t, filepath.Dir(path)); len(left) != 1 {
				t.Errorf("local dir has %v, want only the dump", left)
			}
			if remote := listFiles(t, serverHome); len(remote) != 0 {
				t.Errorf("server has files %v, want none", remote)
			}
		})
	}
}

func TestSaveDumpFailureLeavesNoFile(t *testing.T) {
	tests := []struct {
		mode    string
		wantErr string
	}{
		{mode: "fail", wantErr: "wp db export exited 3: mysqldump: Lost connection"},
		{mode: "empty", wantErr: "empty dump"},
		{mode: "truncated", wantErr: "dump is truncated"},
	}
	for _, shell := range remoteShells(t) {
		for _, tt := range tests {
			t.Run(shell+"/"+tt.mode, func(t *testing.T) {
				_, run := fakeRemote(t, shell, tt.mode)
				dir := t.TempDir()

				_, err := saveDump(filepath.Join(dir, "x.sql"), run)
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				if left := listFiles(t, dir); len(left) != 0 {
					t.Errorf("failed export left %v", left)
				}
			})
		}
	}

	t.Run("transport error mid-stream", func(t *testing.T) {
		dir := t.TempDir()
		_, err := saveDump(filepath.Join(dir, "x.sql"), func(w io.Writer) (internalssh.ExecResult, error) {
			_, _ = io.WriteString(w, "CREATE TABLE wp_po")
			return internalssh.ExecResult{}, errors.New("connection reset")
		})
		if err == nil || !strings.Contains(err.Error(), "connection reset") {
			t.Fatalf("err = %v, want connection reset", err)
		}
		if left := listFiles(t, dir); len(left) != 0 {
			t.Errorf("failed export left %v", left)
		}
	})
}

func TestSaveDumpRefusesOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.sql")
	if err := os.WriteFile(path, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, run := fakeRemote(t, "sh", "ok")
	if _, err := saveDump(path, run); err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("err = %v, want refusal", err)
	}
	if data, _ := os.ReadFile(path); string(data) != "keep" {
		t.Errorf("existing file changed to %q", data)
	}
}

func listFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			files = append(files, p)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func assertPerm(t *testing.T, p string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("mode of %s = %o, want %o", p, got, want)
	}
}
