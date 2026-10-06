package scripts

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestFullBackupTarget runs full-backup.sh from a fake WordPress path with a
// stub wp that writes its export argument, and checks where the dump lands.
func TestFullBackupTarget(t *testing.T) {
	if err := exec.Command("realpath", "-m", "--", "/x/y").Run(); err != nil {
		t.Skip("GNU realpath -m not available")
	}
	script, err := GetScript(ScriptFullBackup)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		backupDir string // WPGO_BACKUP_DIR; "" leaves it unset
		wantDir   string // relative to HOME; "" means refused
	}{
		{name: "default dir", wantDir: "backups/wpgo"},
		{name: "override with space", backupDir: "{HOME}/my backups", wantDir: "my backups"},
		{name: "override inside wp path", backupDir: "{HOME}/public_html/my site/dumps"},
		{name: "relative override resolves in wp path", backupDir: "dumps"},
		{name: "override inside www", backupDir: "{HOME}/www/dumps"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			wpPath := filepath.Join(home, "public_html", "my site")
			for _, d := range []string{wpPath, filepath.Join(home, "www")} {
				if err := os.MkdirAll(d, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			bin := t.TempDir()
			stub := "#!/bin/sh\n[ \"$1 $2\" = \"db export\" ] || exit 2\nprintf 'dump\\n' > \"$3\"\n"
			if err := os.WriteFile(filepath.Join(bin, "wp"), []byte(stub), 0o755); err != nil {
				t.Fatal(err)
			}

			cmd := exec.Command("bash", "-s", "--", "my site", "pre update")
			cmd.Dir = wpPath
			cmd.Stdin = strings.NewReader(script)
			cmd.Env = []string{"HOME=" + home, "PATH=" + bin + ":" + os.Getenv("PATH")}
			if tt.backupDir != "" {
				cmd.Env = append(cmd.Env, "WPGO_BACKUP_DIR="+strings.ReplaceAll(tt.backupDir, "{HOME}", home))
			}
			out, runErr := cmd.Output()

			var result struct {
				Status string `json:"status"`
				Path   string `json:"path"`
				Error  string `json:"error"`
			}
			if err := json.Unmarshal(out, &result); err != nil {
				t.Fatalf("output is not JSON: %v\n%s", err, out)
			}
			dumps := findSQL(t, home)

			if tt.wantDir == "" {
				var exitErr *exec.ExitError
				if !errors.As(runErr, &exitErr) || exitErr.ExitCode() != 64 {
					t.Fatalf("want exit 64, got %v\n%s", runErr, out)
				}
				if result.Status != "error" || !strings.Contains(result.Error, "refusing backup target inside web root") {
					t.Errorf("unexpected result: %+v", result)
				}
				if len(dumps) != 0 {
					t.Errorf("refused backup still wrote %v", dumps)
				}
				return
			}

			if runErr != nil {
				t.Fatalf("backup failed: %v\n%s", runErr, out)
			}
			wantDir := filepath.Join(home, tt.wantDir)
			if result.Status != "ok" || filepath.Dir(result.Path) != wantDir || !filepath.IsAbs(result.Path) {
				t.Fatalf("result = %+v, want ok with path in %s", result, wantDir)
			}
			if !strings.HasPrefix(filepath.Base(result.Path), "my_site_DB_pre_update_") {
				t.Errorf("unexpected filename %s", result.Path)
			}
			if len(dumps) != 1 || dumps[0] != result.Path {
				t.Fatalf("dumps = %v, want [%s]", dumps, result.Path)
			}
			for p, want := range map[string]os.FileMode{result.Path: 0o600, wantDir: 0o700} {
				info, err := os.Stat(p)
				if err != nil {
					t.Fatal(err)
				}
				if got := info.Mode().Perm(); got != want {
					t.Errorf("mode of %s = %o, want %o", p, got, want)
				}
			}
		})
	}
}

func findSQL(t *testing.T, root string) []string {
	t.Helper()
	var found []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".sql") {
			found = append(found, p)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}
