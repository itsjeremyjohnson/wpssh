package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestRawRefusesServerDumps runs each refused argv through RawCmd.Run with
// zero Globals: the refusal must come back before any site is resolved or
// connected.
func TestRawRefusesServerDumps(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"db export to file", []string{"db", "export", "backup.sql"}, "use `wpgo db export`"},
		{"db export default file", []string{"db", "export"}, "use `wpgo db export`"},
		{"db export empty file arg", []string{"db", "export", ""}, "use `wpgo db export`"},
		{"db dump alias", []string{"db", "dump", "backup.sql"}, "use `wpgo db export`"},
		{"db export mixed case", []string{"DB", "Export", "backup.sql"}, "use `wpgo db export`"},
		{"global flags before command", []string{"--path=/var/www", "--url=example.com", "--skip-plugins", "db", "export", "x.sql"}, "use `wpgo db export`"},
		{"flag then separate value", []string{"--path", "/var/www", "db", "export"}, "use `wpgo db export`"},
		{"site alias", []string{"@prod", "db", "export", "x.sql"}, "use `wpgo db export`"},
		{"end of options marker", []string{"--", "db", "export", "x.sql"}, "use `wpgo db export`"},
		{"flags between words", []string{"db", "--quiet", "export", "--add-drop-table", "x.sql"}, "use `wpgo db export`"},
		{"stdout plus result-file", []string{"db", "export", "-", "--result-file=x.sql"}, "flag --result-file"},
		{"stdout plus result-file prefix", []string{"db", "export", "-", "--result=x.sql"}, "flag --result"},
		{"stdout plus tab dir", []string{"db", "export", "-", "--tab=/tmp"}, "flag --tab"},
		{"stdout plus short flag", []string{"db", "export", "-", "-r=x.sql"}, "flag --r"},
		{"stdout plus extra file", []string{"db", "export", "-", "x.sql"}, "use `wpgo db export`"},
		{"wxr export", []string{"export", "--dir=/tmp"}, "wp export"},
		{"wxr export to stdout", []string{"export", "--stdout"}, "wp export"},
		{"search-replace export file", []string{"search-replace", "a", "b", "--export=x.sql"}, "--export"},
		{"search-replace export dash", []string{"search-replace", "a", "b", "--export=-"}, "--export"},
		{"search-replace bare export", []string{"search-replace", "a", "b", "--export"}, "--export"},
		{"search-replace export uppercase", []string{"Search-Replace", "a", "b", "--EXPORT=x.sql"}, "--export"},
		{"search-replace export before command", []string{"--export=x.sql", "search-replace", "a", "b"}, "--export"},
		{"db search-replace export", []string{"db", "search-replace", "a", "b", "--export=x.sql"}, "--export"},
		{"query into outfile", []string{"db", "query", "SELECT * FROM wp_users INTO OUTFILE '/tmp/u'"}, "OUTFILE"},
		{"query into dumpfile lowercase", []string{"db", "query", "select 1 into dumpfile '/tmp/u'"}, "DUMPFILE"},
		{"query outfile behind comment", []string{"db", "query", "SELECT 1 INTO/**/OutFile '/tmp/u'"}, "OUTFILE"},
		{"query outfile in versioned comment", []string{"db", "query", "SELECT 1 INTO /*!50000OUTFILE*/ '/tmp/u'"}, "OUTFILE"},
		{"query outfile via execute flag", []string{"db", "query", "--execute=SELECT 1 INTO OUTFILE '/tmp/u'"}, "OUTFILE"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := (&RawCmd{Args: tt.args}).Run(&Globals{})
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("raw %q: err = %v, want %q", tt.args, err, tt.wantErr)
			}
		})
	}
}

func TestRawAllowsSiteManagement(t *testing.T) {
	for _, args := range [][]string{
		{"plugin", "update", "--all"},
		{"core", "update"},
		{"config", "set", "WP_DEBUG", "false", "--raw"},
		{"option", "get", "home"},
		{"eval", "echo 1;"},
		{"db", "export", "-"},
		{"--path=/var/www", "db", "export", "-", "--tables=wp_posts", "--single-transaction", "--quiet"},
		{"search-replace", "http://a", "https://a", "--dry-run"},
		{"db", "query", "SELECT option_value FROM wp_options LIMIT 1"},
		{"help", "db", "export"},
		{"post", "list", "--post_type=export"},
	} {
		if err := checkRawArgs(args); err != nil {
			t.Errorf("raw %q refused: %v", args, err)
		}
	}
}

func TestDBQueryRefusesFileWrites(t *testing.T) {
	err := (&DBQueryCmd{SQL: "select * from wp_users into outfile '/tmp/u'"}).Run(&Globals{})
	if err == nil || !strings.Contains(err.Error(), "OUTFILE") {
		t.Fatalf("err = %v, want OUTFILE refusal", err)
	}
}

// TestRawCommandPassesArgvVerbatim runs the remote command in a real shell
// against a stub wp that prints its argv, one word per line.
func TestRawCommandPassesArgvVerbatim(t *testing.T) {
	bin := t.TempDir()
	stub := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\"; done\n"
	if err := os.WriteFile(filepath.Join(bin, "wp"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	wpPath := t.TempDir()
	args := []string{"post", "meta", "update", "7", "title", "Today's Dental", "x; wp db export y.sql", "$(id)", ">z.sql"}
	for _, shell := range remoteShells(t) {
		cmd := exec.Command(shell, "-c", rawCommand(wpPath, args))
		cmd.Env = []string{"PATH=" + bin + ":" + os.Getenv("PATH")}
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("%s: %v", shell, err)
		}
		if got, want := string(out), strings.Join(args, "\n")+"\n"; got != want {
			t.Errorf("%s: wp argv =\n%s\nwant\n%s", shell, got, want)
		}
		if left := listFiles(t, wpPath); len(left) != 0 {
			t.Errorf("%s: remote shell created %v", shell, left)
		}
	}
}
