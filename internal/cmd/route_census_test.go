package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/alecthomas/kong"
)

// censusSkip lists commands the census does not run: they talk to no site,
// prompt, or, like raw and eval, take a wp command line or PHP by design.
var censusSkip = []string{"help", "setup", "version", "sites", "raw", "eval"}

// wpengineSplitting is every command slot that still reaches wp on WP Engine
// as a quoted word, which the gateway strips and re-splits. Moving one to a
// whole-value route removes it here; a new command must not add one.
var wpengineSplitting = []string{
	"cache delete <group>", "cache delete <key>", "cache get <group>", "cache get <key>",
	"cache set <group>", "cache set <key>", "cache set <value>",
	"config delete <name>", "config get <name>", "config has <name>", "config set <name>",
	"config set <value>",
	"core update --version",
	"cron event delete <hook>", "cron event run <hook>", "cron event schedule <hook>",
	"cron event schedule <next-run>", "cron event schedule <recurrence>",
	"cron event unschedule <hook>",
	"db query <sql>", "db search <term>",
	"media import <file>",
	"menu create <name>", "menu item add-custom <menu>", "menu item add-custom <title>",
	"menu item add-custom <url>", "menu item add-post <menu>", "menu item list <menu>",
	"option delete <key>", "option get <key>", "option list --search",
	"option patch <action>", "option patch <key>", "option patch <key-path>",
	"option patch <value>", "option pluck <key>", "option pluck <key-path>",
	"plugin activate <plugin>", "plugin auto-updates disable <plugin>",
	"plugin auto-updates enable <plugin>", "plugin auto-updates status <plugin>",
	"plugin deactivate <plugin>", "plugin delete <plugin>", "plugin get <plugin>",
	"plugin install <plugin>", "plugin is-active <plugin>", "plugin is-installed <plugin>",
	"plugin list --status", "plugin search <term>", "plugin status <plugin>",
	"plugin update <plugin>", "plugin verify-checksums <plugin>",
	"post create --post_title", "post list --post_type", "post meta delete <key>",
	"post meta get <key>",
	"rewrite structure <permastruct>",
	"role create <display-name>", "role create <role>", "role delete <role>",
	"role exists <role>",
	"search-replace <new>", "search-replace <old>",
	"theme activate <theme>", "theme auto-updates disable <theme>",
	"theme auto-updates enable <theme>", "theme auto-updates status <theme>",
	"theme delete <theme>", "theme get <theme>", "theme install <theme>",
	"theme is-active <theme>", "theme is-installed <theme>", "theme list --status",
	"theme search <term>", "theme status <theme>", "theme update <theme>",
	"transient delete <key>", "transient get <key>", "transient set <key>",
	"transient set <value>",
	"user create <email>", "user create <login>", "user create --role",
	"user delete <user>", "user exists <user>", "user get <user>", "user list --role",
	"user meta delete <key>", "user meta delete <user>", "user meta get <key>",
	"user meta get <user>", "user meta list <user>", "user reset-password <user>",
	"user set-role <role>", "user set-role <user>", "user update <user>",
}

// censusSlot is one user-supplied argument or flag of a command, filled with
// a sentinel holding a space.
type censusSlot struct {
	name     string // "<key>" or "--flag"
	sentinel string
}

// TestValueRouteCensus runs every site command with a distinct sentinel in
// each string argument and flag, on a standard and a WP Engine site, and
// classifies how each sentinel reaches the server. Run with -v to print the
// census. It checks the standard route always quotes a value whole and that
// WP Engine splits no value outside wpengineSplitting.
func TestValueRouteCensus(t *testing.T) {
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer devNull.Close()
	stdin, stdout := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = devNull, devNull
	defer func() { os.Stdin, os.Stdout = stdin, stdout }()

	parser, err := kong.New(&CLI{}, kong.Name("wpgo"), kong.Exit(func(int) {}), censusVars)
	if err != nil {
		t.Fatal(err)
	}
	var rows, splitting []string
	for _, leaf := range parser.Model.Leaves(false) {
		path := strings.TrimPrefix(leaf.FullPath(), "wpgo ")
		if slices.Contains(censusSkip, strings.Fields(path)[0]) {
			continue
		}
		argv, slots := censusArgv(leaf)
		if len(slots) == 0 {
			continue
		}
		routes := map[string][]remoteExec{}
		for _, host := range []string{"standard", "wpengine"} {
			execs := sshSite(t, host)
			noRateLimit(t)
			var cli CLI
			p, err := kong.New(&cli, kong.Name("wpgo"), kong.Exit(func(int) {}), censusVars)
			if err != nil {
				t.Fatal(err)
			}
			ctx, err := p.Parse(append([]string{"--site", "site", "--no-cache", "--yes"}, argv...))
			if err != nil {
				t.Fatalf("%s: parse %q: %v", path, argv, err)
			}
			_, _ = captureStderr(t, func() error { return ctx.Run(&cli.Globals) })
			routes[host] = execs()
		}
		for _, s := range slots {
			std := censusRoute(routes["standard"], s.sentinel)
			wpe := censusRoute(routes["wpengine"], s.sentinel)
			slot := path + " " + s.name
			rows = append(rows, fmt.Sprintf("%-40s standard=%-12s wpengine=%s", slot, std, wpe))
			if std == "unquoted" || wpe == "unquoted" {
				t.Errorf("%s reaches the remote shell unquoted", slot)
			}
			if wpe == "quoted-word" {
				splitting = append(splitting, slot)
			}
		}
	}
	sort.Strings(rows)
	t.Logf("census:\n%s", strings.Join(rows, "\n"))
	for _, slot := range splitting {
		if !slices.Contains(wpengineSplitting, slot) {
			t.Errorf("%s is split by the WP Engine gateway; route it as a whole value", slot)
		}
	}
	for _, slot := range wpengineSplitting {
		if !slices.Contains(splitting, slot) {
			t.Errorf("%s no longer splits on WP Engine; remove it from wpengineSplitting", slot)
		}
	}
}

// censusArgv returns the argv that runs leaf with a sentinel in every string
// argument and flag, "1" in every integer one, and an enum's first value.
func censusArgv(leaf *kong.Node) (argv []string, slots []censusSlot) {
	argv = strings.Fields(strings.TrimPrefix(leaf.FullPath(), "wpgo "))
	fill := func(v *kong.Value, name string, flag bool) {
		var val string
		switch {
		case v.Enum != "":
			val = strings.Split(v.Enum, ",")[0]
		case v.Target.Kind() == reflect.String || v.Target.Kind() == reflect.Slice:
			val = fmt.Sprintf("sv%d x", len(slots))
			slots = append(slots, censusSlot{name, val})
		case v.Target.Kind() >= reflect.Int && v.Target.Kind() <= reflect.Uint64:
			val = "1"
		default:
			return
		}
		if flag {
			argv = append(argv, "--"+v.Name+"="+val)
		} else {
			argv = append(argv, val)
		}
	}
	for _, a := range leaf.Positional {
		fill(a, "<"+a.Name+">", false)
	}
	for _, f := range leaf.Flags {
		if f.Hidden || f.Target.Kind() == reflect.Bool {
			continue
		}
		fill(f.Value, "--"+f.Name, true)
	}
	return argv, slots
}

// censusRoute classifies how sentinel reached the server in execs.
func censusRoute(execs []remoteExec, sentinel string) string {
	for _, e := range execs {
		switch {
		case strings.Contains(e.command, evalArg(sentinel)):
			return "eval-file"
		case strings.Contains(e.command, "'"+sentinel+"'"):
			return "quoted-word"
		case strings.Contains(e.command, sentinel):
			return "unquoted"
		case strings.Contains(e.stdin, sentinel):
			return "stdin"
		}
	}
	if len(execs) == 0 {
		return "not-run"
	}
	return "not-sent"
}

// censusVars fills the variables main passes to kong.
var censusVars = kong.Vars{"version": "census", "commit": "", "date": ""}

// noRateLimit lifts the default 500ms spacing between commands to the site
// sshSite serves, so the census runs every command quickly.
func noRateLimit(t *testing.T) {
	t.Helper()
	home := os.Getenv("HOME")
	sshConfig, err := os.ReadFile(filepath.Join(home, ".ssh", "config"))
	if err != nil {
		t.Fatal(err)
	}
	_, port, _ := strings.Cut(string(sshConfig), "Port ")
	port, _, _ = strings.Cut(port, "\n")
	cfg := `{"rate_limits":{"127.0.0.1:` + port + `":{"delay":0,"max_conns":3}}}`
	if err := os.WriteFile(filepath.Join(home, ".config", "wpgo", "config.json"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
}
