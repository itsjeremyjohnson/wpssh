package cmd

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
		{"repeated delimiters", []string{"--", "--", "--", "db", "export", "backup.sql"}, "use `wpgo db export`"},
		{"alias then repeated delimiters", []string{"@prod", "--", "--", "db", "export", "backup.sql"}, "use `wpgo db export`"},
		{"flags between words", []string{"db", "--quiet", "export", "--add-drop-table", "x.sql"}, "use `wpgo db export`"},
		{"quoted command word", []string{"db", "'export'", "x.sql"}, "use `wpgo db export`"},
		{"backslash in command word", []string{"db", `ex\port`, "x.sql"}, "use `wpgo db export`"},
		{"sql alias export", []string{"sql", "export", "x.sql"}, "use `wpgo db export`"},
		{"sql alias dump with site", []string{"@prod", "sql", "dump", "x.sql"}, "use `wpgo db export`"},
		{"stdout plus result-file", []string{"db", "export", "-", "--result-file=x.sql"}, "flag --result-file"},
		{"stdout plus result-file prefix", []string{"db", "export", "-", "--result=x.sql"}, "flag --result"},
		{"stdout plus tab dir", []string{"db", "export", "-", "--tab=/tmp"}, "flag --tab"},
		{"stdout plus short flag", []string{"db", "export", "-", "-r=x.sql"}, "flag --r"},
		{"stdout plus extra file", []string{"db", "export", "-", "x.sql"}, "use `wpgo db export`"},
		{"stdout plus defaults", []string{"db", "export", "-", "--defaults"}, "flag --defaults"},
		{"tables value is a flag", []string{"db", "export", "-", "--tables=--result-file=x.sql"}, "--tables entry"},
		{"tables entry is a flag", []string{"db", "export", "-", "--tables=a,--tab=/tmp"}, "--tables entry"},
		{"exclude_tables entry is a flag", []string{"db", "export", "-", "'--exclude_tables=a, -r'"}, "--exclude_tables entry"},
		{"db cli", []string{"db", "cli"}, "wp db cli"},
		{"db connect", []string{"db", "connect"}, "wp db connect"},
		{"sql cli", []string{"sql", "cli"}, "wp db cli"},
		{"sql connect", []string{"@prod", "sql", "connect"}, "wp db connect"},
		{"exec global", []string{"option", "get", "home", `--exec='file_put_contents("x.sql", 1);'`}, "--exec"},
		{"exec global spaced", []string{"--exec", "'echo 1;'", "option", "get", "home"}, "--exec"},
		{"require global", []string{"--require=/tmp/dump.php", "option", "get", "home"}, "--require"},
		{"require global spaced", []string{"plugin", "list", "--require", "/tmp/dump.php"}, "--require"},
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
		{"query outfile split by quotes", []string{"db", "query", `'SELECT 1 INTO OUT'"FILE '/tmp/u'"`}, "OUTFILE"},
		{"sql alias query outfile", []string{"sql", "query", `"SELECT 1 INTO OUTFILE '/tmp/u'"`}, "OUTFILE"},
		{"semicolon chain", []string{"option", "get", "home;", "wp", "db", "export", "x.sql"}, "';'"},
		{"semicolon inside one arg", []string{"option", "get", "x; wp db export y.sql"}, "';'"},
		{"and chain", []string{"option get home && wp export"}, "'&'"},
		{"pipe", []string{"post", "list", "|", "tee", "x.sql"}, "'|'"},
		{"redirect", []string{"post", "list", ">x.sql"}, "'>'"},
		{"append redirect", []string{"post", "list", ">>", "x.sql"}, "'>'"},
		{"input redirect", []string{"db", "import", "<x.sql"}, "'<'"},
		{"newline", []string{"option", "get", "home\nwp db export x.sql"}, `'\n'`},
		{"command substitution", []string{"option", "get", "$(wp db export x.sql)"}, "'$'"},
		{"backticks", []string{"option", "get", "`wp db export x.sql`"}, "'`'"},
		{"variable", []string{"option", "get", "$HOME"}, "'$'"},
		{"dollar inside double quotes", []string{"option", "get", `"$(wp db export x.sql)"`}, "$ inside double quotes"},
		{"backtick inside double quotes", []string{"option", "get", "\"`id`\""}, "` inside double quotes"},
		{"brace expansion", []string{"db", "export", "{-,x.sql}"}, "'{'"},
		{"comment", []string{"db", "export", "-", "#", "x"}, "comment"},
		{"unbalanced single quote", []string{"option", "get", "'home"}, "unbalanced single quote"},
		{"unbalanced double quote", []string{"option", "get", `"home`}, "unbalanced double quote"},
		{"trailing backslash", []string{"option", "get", `home\`}, "trailing backslash"},
		{"line continuation", []string{"option", "get", "home\\\nx"}, "line continuation"},
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
		{"eval", "'echo 1;'"},
		{"eval", "'echo 1;'", "--require=/tmp/helper.php"},
		{"--exec='echo 1;'", "eval-file", "x.php"},
		{"db", "export", "-"},
		{"--", "db", "export", "-"},
		{"--path=/var/www", "db", "export", "-", "--tables=wp_posts,wp_options", "--single-transaction", "--quiet"},
		{"db", "export", "-", "--exclude_tables=wp_users", "--no-defaults"},
		{"search-replace", "http://a", "https://a", "--dry-run"},
		{"db", "query", "'SELECT option_value FROM wp_options LIMIT 1'"},
		{"sql", "query", `"SELECT 'a' AS x"`},
		{"help", "db", "export"},
		{"post", "list", "--post_type=export"},
	} {
		for _, nested := range []bool{false, true} {
			if err := checkRawArgs(args, nested); err != nil {
				t.Errorf("raw %q (nested=%v) refused: %v", args, nested, err)
			}
		}
	}
	// Quoting that is one layer deep passes on a standard site only.
	for _, args := range [][]string{
		{"post", "meta", "update", "7", "title", `"Today's Dental"`},
		{"option", "get", `\$home`},
	} {
		if err := checkRawArgs(args, false); err != nil {
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

// shlexQuote is Python's shlex.quote for ASCII input, which charlie-hermes
// uses to pre-quote eval PHP for raw.
func shlexQuote(s string) string {
	if s == "" {
		return "''"
	}
	if !regexp.MustCompile(`[^\w@%+=:,./-]`).MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// fakeSite registers one site of hostType in a temp HOME, with its WordPress
// path in a temp dir, and returns that path. Run with DryRun then prints the
// remote command instead of connecting.
func fakeSite(t *testing.T, hostType string) (wpPath string) {
	t.Helper()
	home := t.TempDir()
	wpPath = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	files := map[string]string{
		".ssh/config":             "Host site\n  HostName 192.0.2.10\n  User u\n",
		".config/wpgo/sites.json": `{"sites":{"site":{"wp_path":"` + wpPath + `","host_type":"` + hostType + `"}}}`,
	}
	for name, body := range files {
		p := filepath.Join(home, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return wpPath
}

// dryRunRaw runs RawCmd.Run in dry-run mode against the fake site and returns
// the remote command it would send.
func dryRunRaw(t *testing.T, args []string) (string, error) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr := os.Stderr
	os.Stderr = w
	runErr := (&RawCmd{Args: args}).Run(&Globals{Site: "site", DryRun: true, NoCache: true})
	os.Stderr = stderr
	w.Close()
	out, err := io.ReadAll(r)
	r.Close()
	if err != nil {
		t.Fatal(err)
	}
	_, cmd, _ := strings.Cut(string(out), "[dry-run] ")
	return strings.TrimSuffix(cmd, "\n"), runErr
}

// TestRawEvalShlexQuotedReachesWPVerbatim builds raw args exactly as
// charlie-hermes does for a standard site, runs the command RawCmd would send
// through sh and dash against a stub wp, and checks wp receives the PHP bytes
// unchanged.
func TestRawEvalShlexQuotedReachesWPVerbatim(t *testing.T) {
	bin := t.TempDir()
	stub := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\0' \"$a\"; done\n"
	if err := os.WriteFile(filepath.Join(bin, "wp"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	wpPath := fakeSite(t, "standard")
	shells := remoteShells(t)
	for _, php := range []string{
		"echo 1+1;",
		`$x = get_option('home'); echo "$x (ok)\n";`,
		"$p = get_post(7); echo $p->post_title . ' isn\\'t `x` & | > ;';\nreturn;",
		"echo json_encode(['a' => \"b's\"]);",
	} {
		args := []string{"eval", shlexQuote(php)}
		cmd, err := dryRunRaw(t, args)
		if err != nil {
			t.Fatalf("raw %q refused: %v", args, err)
		}
		for _, shell := range shells {
			c := exec.Command(shell, "-c", cmd)
			c.Env = []string{"PATH=" + bin + ":" + os.Getenv("PATH")}
			out, err := c.Output()
			if err != nil {
				t.Fatalf("%s -c %q: %v", shell, cmd, err)
			}
			if got, want := string(out), "eval\x00"+php+"\x00"; got != want {
				t.Errorf("%s: wp argv = %q, want %q", shell, got, want)
			}
		}
		if left := listFiles(t, wpPath); len(left) != 0 {
			t.Errorf("remote shell created %v", left)
		}
	}
}

// TestRawWPEngineChecksSecondParse covers WP Engine, whose gateway parses each
// word again: words that are harmless after one parse are refused there, while
// a double-quoted eval passes on both adapters.
func TestRawWPEngineChecksSecondParse(t *testing.T) {
	php := `$x = get_option('home'); echo "$x"; // isn't > x.sql`
	tests := []struct {
		name    string
		args    []string
		wantErr string // on wpengine; standard must pass
	}{
		{"hidden export word", []string{"db", "'export x.sql'"}, "use `wpgo db export`"},
		{"hidden chain", []string{"option", "get", "'home; wp db export y.sql'"}, "';'"},
		{"hidden export flag", []string{"search-replace", "a", "b", `"--exp''ort=x.sql"`}, "--export"},
		{"hidden result-file", []string{"db", "export", "-", `"--tables=a --result-file=x.sql"`}, "flag --result-file"},
		{"double-quoted eval", []string{"eval", shlexQuote(shlexQuote(php))}, ""},
		{"plain command", []string{"plugin", "list", "--status=active"}, ""},
	}
	for _, hostType := range []string{"standard", "wpengine"} {
		for _, tt := range tests {
			t.Run(hostType+"/"+tt.name, func(t *testing.T) {
				fakeSite(t, hostType)
				cmd, err := dryRunRaw(t, tt.args)
				want := ""
				if hostType == "wpengine" {
					want = tt.wantErr
				}
				if want == "" {
					if err != nil || !strings.HasSuffix(cmd, " && wp "+strings.Join(tt.args, " ")) {
						t.Fatalf("raw %q: cmd = %q, err = %v; want sent unchanged", tt.args, cmd, err)
					}
					return
				}
				if err == nil || !strings.Contains(err.Error(), want) || cmd != "" {
					t.Fatalf("raw %q: cmd = %q, err = %v; want refusal %q before sending", tt.args, cmd, err, want)
				}
			})
		}
	}
}
