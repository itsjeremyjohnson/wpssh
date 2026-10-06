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
	"regexp"
	"strings"
	"testing"
	"time"

	internalssh "github.com/builtbyrobben/wpssh/internal/ssh"
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
// In mode "ok" the stub prints dump.
func fakeRemote(t *testing.T, shell, mode, dump string) (home string, run func(io.Writer) (internalssh.ExecResult, error)) {
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
		cmd := exec.Command(shell, "-c", dbExportStdout(wpPath))
		cmd.Env = []string{"HOME=" + home, "PATH=" + bin + ":" + os.Getenv("PATH"), "STUB_MODE=" + mode, "STUB_DUMP=" + dump}
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
	now := time.Date(2026, 10, 6, 12, 0, 0, 123_000_000, time.UTC)

	t.Run("home default", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("WPGO_LOCAL_BACKUP_DIR", "")
		got, err := defaultDumpPath("my site", "pre update", now)
		if err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(home, "wpgo-backups", "my_site")
		want := regexp.MustCompile(`^my_site_DB_pre_update_20261006_120000_123_[0-9a-f]{6}\.sql$`)
		if filepath.Dir(got) != dir || !want.MatchString(filepath.Base(got)) {
			t.Fatalf("path = %s, want %s/%s", got, dir, want)
		}
		assertPerm(t, filepath.Join(home, "wpgo-backups"), 0o700)
		assertPerm(t, dir, 0o700)
	})

	t.Run("same millisecond gets distinct names", func(t *testing.T) {
		t.Setenv("WPGO_LOCAL_BACKUP_DIR", t.TempDir())
		a, err := defaultDumpPath("acme", "export", now)
		if err != nil {
			t.Fatal(err)
		}
		b, err := defaultDumpPath("acme", "export", now)
		if err != nil {
			t.Fatal(err)
		}
		if a == b {
			t.Fatalf("two backups at the same instant both named %s", a)
		}
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
		if want := filepath.Join(base, "acme"); filepath.Dir(got) != want {
			t.Fatalf("path = %s, want it in %s", got, want)
		}
		assertPerm(t, base, 0o700)
	})
}

// TestSaveDumpStreamsToOperator runs the default export command against a
// local stand-in for the server and checks the dump lands only locally.
func TestSaveDumpStreamsToOperator(t *testing.T) {
	dumps := map[string]string{
		"lf":                   completeDump,
		"crlf":                 "CREATE TABLE wp_posts (ID int);\r\n-- Dump completed on 2026-10-06 12:00:00\r\n",
		"trailing blank lines": completeDump + "\n \n\t\n",
	}
	for _, shell := range remoteShells(t) {
		for name, dump := range dumps {
			t.Run(shell+"/"+name, func(t *testing.T) {
				sum := sha256.Sum256([]byte(dump))
				wantSHA := hex.EncodeToString(sum[:])
				serverHome, run := fakeRemote(t, shell, "ok", dump)
				path := filepath.Join(t.TempDir(), "site_DB_export.sql")

				got, err := saveDump(path, run)
				if err != nil {
					t.Fatal(err)
				}
				if got.Path != path || got.Bytes != int64(len(dump)) || got.SHA256 != wantSHA {
					t.Errorf("saveDump = %+v, want path %s, %d bytes, sha %s", got, path, len(dump), wantSHA)
				}
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if string(data) != dump {
					t.Errorf("local dump = %q, want %q", data, dump)
				}
				assertPerm(t, path, 0o600)
				if left := listFiles(t, filepath.Dir(path)); len(left) != 1 {
					t.Errorf("local dir has %v, want only the dump", left)
				}
				if remote := listFiles(t, serverHome); len(remote) != 0 {
					t.Errorf("server has files %v, want none", remote)
				}

				var out bytes.Buffer
				if err := writeDump(&out, run); err != nil {
					t.Fatalf("stdout export: %v", err)
				}
				if out.String() != dump {
					t.Errorf("stdout export = %q, want %q", out.String(), dump)
				}
			})
		}
	}
}

// TestDumpFailures checks that file exports leave no file and stdout exports
// return an error for each way a dump can be incomplete.
func TestDumpFailures(t *testing.T) {
	tests := []struct {
		name    string
		mode    string
		dump    string
		wantErr string
	}{
		{name: "remote exit", mode: "fail", wantErr: "wp db export exited 3: mysqldump: Lost connection"},
		{name: "empty", mode: "ok", dump: "", wantErr: "empty dump"},
		{name: "truncated", mode: "ok", dump: "CREATE TABLE wp_posts (ID int);\nINSERT INTO wp_po", wantErr: "dump is truncated"},
		{
			name:    "trailer inside data",
			mode:    "ok",
			dump:    "INSERT INTO wp_posts VALUES (1,'-- Dump completed on 2026-10-06');\nINSERT INTO wp_posts VALUES (2,'cut",
			wantErr: "dump is truncated",
		},
		{
			// The kept tail starts at a marker in the middle of a long line.
			name:    "trailer inside a final line longer than the tail",
			mode:    "ok",
			dump:    "INSERT INTO wp_posts VALUES ('" + dumpTrailer + strings.Repeat("y", dumpTailSize-len(dumpTrailer)),
			wantErr: "dump is truncated",
		},
	}
	for _, shell := range remoteShells(t) {
		for _, tt := range tests {
			t.Run(shell+"/"+tt.name, func(t *testing.T) {
				_, run := fakeRemote(t, shell, tt.mode, tt.dump)
				dir := t.TempDir()

				_, err := saveDump(filepath.Join(dir, "x.sql"), run)
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("file export err = %v, want %q", err, tt.wantErr)
				}
				if left := listFiles(t, dir); len(left) != 0 {
					t.Errorf("failed export left %v", left)
				}

				if err := writeDump(io.Discard, run); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("stdout export err = %v, want %q", err, tt.wantErr)
				}
			})
		}
	}

	t.Run("transport error mid-stream", func(t *testing.T) {
		dir := t.TempDir()
		run := func(w io.Writer) (internalssh.ExecResult, error) {
			_, _ = io.WriteString(w, "CREATE TABLE wp_po")
			return internalssh.ExecResult{}, errors.New("connection reset")
		}
		_, err := saveDump(filepath.Join(dir, "x.sql"), run)
		if err == nil || !strings.Contains(err.Error(), "connection reset") {
			t.Fatalf("err = %v, want connection reset", err)
		}
		if left := listFiles(t, dir); len(left) != 0 {
			t.Errorf("failed export left %v", left)
		}
		if err := writeDump(io.Discard, run); err == nil || !strings.Contains(err.Error(), "connection reset") {
			t.Fatalf("stdout export err = %v, want connection reset", err)
		}
	})
}

func TestSaveDumpRefusesOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.sql")
	if err := os.WriteFile(path, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, run := fakeRemote(t, "sh", "ok", completeDump)
	if _, err := saveDump(path, run); err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("err = %v, want refusal", err)
	}
	if data, _ := os.ReadFile(path); string(data) != "keep" {
		t.Errorf("existing file changed to %q", data)
	}
}

// TestSaveDumpKeepsFileCreatedDuringStream covers a destination that appears
// after the up-front existence check, e.g. a concurrent backup.
func TestSaveDumpKeepsFileCreatedDuringStream(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.sql")
	_, err := saveDump(path, func(w io.Writer) (internalssh.ExecResult, error) {
		half := len(completeDump) / 2
		_, _ = io.WriteString(w, completeDump[:half])
		if err := os.WriteFile(path, []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(w, completeDump[half:])
		return internalssh.ExecResult{}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("err = %v, want refusal", err)
	}
	if data, _ := os.ReadFile(path); string(data) != "keep" {
		t.Errorf("existing file changed to %q", data)
	}
	if left := listFiles(t, dir); len(left) != 1 {
		t.Errorf("dir has %v, want only the pre-existing file", left)
	}
}

// TestSaveDumpReportsLeftoverPartial makes the dir read-only mid-stream so
// the failed dump's temp file cannot be removed; the error must name it.
func TestSaveDumpReportsLeftoverPartial(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	_, err := saveDump(filepath.Join(dir, "x.sql"), func(w io.Writer) (internalssh.ExecResult, error) {
		_, _ = io.WriteString(w, "CREATE TABLE wp_po")
		if err := os.Chmod(dir, 0o500); err != nil {
			t.Fatal(err)
		}
		return internalssh.ExecResult{}, errors.New("connection reset")
	})
	left := listFiles(t, dir)
	if len(left) != 1 {
		t.Fatalf("dir has %v, want the one partial dump", left)
	}
	for _, want := range []string{"connection reset", "could not remove partial dump " + left[0]} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to contain %q", err, want)
		}
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
