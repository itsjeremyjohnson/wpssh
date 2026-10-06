package wpcli

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDBExportCommandContainsGuard(t *testing.T) {
	got := DBExport("~/public_html", "backup.sql", "site_DB.sql")
	for _, want := range []string{
		`cd "$HOME"/'public_html' && umask 077 && `,
		`WPGO_DIR="$(realpath -m -- "${WPGO_BACKUP_DIR:-$HOME/backups/wpgo}")"`,
		`WPGO_DEST="$(realpath -m -- "$WPGO_DIR"/'backup.sql')"`,
		`"$WPGO_WEBROOT" "$(realpath -m -- "$HOME/public_html")" "$(realpath -m -- "$HOME/www")"`,
		`exit 64;;`,
		`mkdir -p -m 0700 -- "$WPGO_DIR"`,
		`wp db export "$WPGO_DEST" && chmod 0600 -- "$WPGO_DEST"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("DBExport() missing %q\ngot: %s", want, got)
		}
	}
}

func TestDBExportStdoutWritesNoFile(t *testing.T) {
	got := DBExport("/var/www/html", "-", "site_DB.sql")
	want := "cd '/var/www/html' && wp db export '-'"
	if got != want {
		t.Errorf("DBExport(-) = %q, want %q", got, want)
	}
}

// TestDBExportTargets runs the built command in a shell against a fake home
// with a stub wp that writes its export argument, and checks where dumps land.
func TestDBExportTargets(t *testing.T) {
	if err := exec.Command("realpath", "-m", "--", "/x/y").Run(); err != nil {
		t.Skip("GNU realpath -m not available")
	}

	tests := []struct {
		name      string
		file      string
		backupDir string // WPGO_BACKUP_DIR; "" leaves it unset
		wantPath  string // relative to HOME; "" means refused
	}{
		{name: "default name", file: "", wantPath: "backups/wpgo/site_DB.sql"},
		{name: "relative file", file: "backup.sql", wantPath: "backups/wpgo/backup.sql"},
		{name: "relative file with space", file: "pre update.sql", wantPath: "backups/wpgo/pre update.sql"},
		{name: "relative traversal into wp path", file: "../../public_html/my site/x.sql"},
		{name: "absolute inside wp path", file: "{HOME}/public_html/my site/x.sql"},
		{name: "absolute inside public_html", file: "{HOME}/public_html/x.sql"},
		{name: "absolute inside www", file: "{HOME}/www/x.sql"},
		{name: "tilde inside public_html", file: "~/public_html/x.sql"},
		{name: "absolute outside web root", file: "{HOME}/private/x.sql", wantPath: "private/x.sql"},
		{name: "backup dir override", file: "x.sql", backupDir: "{HOME}/my dumps", wantPath: "my dumps/x.sql"},
		{name: "relative backup dir override lands in wp path", file: "x.sql", backupDir: "dumps"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			wpPath := filepath.Join(home, "public_html", "my site")
			mustMkdir(t, wpPath)
			mustMkdir(t, filepath.Join(home, "www"))
			mustMkdir(t, filepath.Join(home, "private"))
			bin := stubWP(t)

			file := strings.ReplaceAll(tt.file, "{HOME}", home)
			cmd := exec.Command("sh", "-c", DBExport(wpPath, file, "site_DB.sql"))
			cmd.Env = []string{"HOME=" + home, "PATH=" + bin + ":" + os.Getenv("PATH")}
			if tt.backupDir != "" {
				cmd.Env = append(cmd.Env, "WPGO_BACKUP_DIR="+strings.ReplaceAll(tt.backupDir, "{HOME}", home))
			}
			out, err := cmd.CombinedOutput()

			dumps := findDumps(t, home)
			if tt.wantPath == "" {
				var exitErr *exec.ExitError
				if !errors.As(err, &exitErr) || exitErr.ExitCode() != 64 {
					t.Fatalf("want exit 64, got err=%v output=%s", err, out)
				}
				if !strings.Contains(string(out), "refusing to write database export inside web root") {
					t.Errorf("missing refusal message: %s", out)
				}
				if len(dumps) != 0 {
					t.Errorf("refused export still wrote %v", dumps)
				}
				return
			}
			if err != nil {
				t.Fatalf("export failed: %v\n%s", err, out)
			}
			want := filepath.Join(home, tt.wantPath)
			if len(dumps) != 1 || dumps[0] != want {
				t.Fatalf("dumps = %v, want [%s]", dumps, want)
			}
			if strings.HasPrefix(dumps[0], wpPath+"/") {
				t.Fatalf("dump written inside wp path: %s", dumps[0])
			}
			assertMode(t, want, 0o600)
			if tt.backupDir == "" {
				assertMode(t, filepath.Join(home, "backups", "wpgo"), 0o700)
			}
		})
	}
}

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

// stubWP installs a fake wp that handles `wp db export <file>` by writing a
// small file, honouring the caller's umask like the real wp-cli.
func stubWP(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\n[ \"$1 $2\" = \"db export\" ] || exit 2\nprintf 'dump\\n' > \"$3\"\n"
	if err := os.WriteFile(filepath.Join(bin, "wp"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func findDumps(t *testing.T, root string) []string {
	t.Helper()
	var dumps []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".sql") {
			dumps = append(dumps, p)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return dumps
}

func assertMode(t *testing.T, p string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("mode of %s = %o, want %o", p, got, want)
	}
}
