package cmd

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// valueCase is a command whose last argument is a user value, and the wp
// argv it must produce.
type valueCase struct {
	name string
	run  func(g *Globals, value string) error
	argv func(value string) []string
}

var valueCases = []valueCase{
	{
		"post meta update",
		func(g *Globals, v string) error { return (&PostMetaUpdateCmd{ID: 1, Key: "k", Value: v}).Run(g) },
		func(v string) []string { return []string{"post", "meta", "update", "1", "k", v} },
	},
	{
		"user meta update",
		func(g *Globals, v string) error {
			return (&UserMetaUpdateCmd{User: "admin", Key: "k", Value: v}).Run(g)
		},
		func(v string) []string { return []string{"user", "meta", "update", "admin", "k", v} },
	},
	{
		"option update",
		func(g *Globals, v string) error { return (&OptionUpdateCmd{Key: "k", Value: v}).Run(g) },
		func(v string) []string { return []string{"option", "update", "k", v} },
	},
	{
		"option add",
		func(g *Globals, v string) error { return (&OptionAddCmd{Key: "k", Value: v}).Run(g) },
		func(v string) []string { return []string{"option", "add", "k", v} },
	},
	{
		"config set",
		func(g *Globals, v string) error { return (&ConfigSetCmd{Name: "K", Value: v}).Run(g) },
		func(v string) []string { return []string{"config", "set", "K", v} },
	},
	{
		"cache set",
		func(g *Globals, v string) error { return (&CacheSetCmd{Key: "k", Value: v}).Run(g) },
		func(v string) []string { return []string{"cache", "set", "k", v} },
	},
	{
		"cache set with group",
		func(g *Globals, v string) error { return (&CacheSetCmd{Key: "k", Value: v, Group: "g"}).Run(g) },
		func(v string) []string { return []string{"cache", "set", "k", v, "g"} },
	},
	{
		"transient set",
		func(g *Globals, v string) error {
			return (&TransientSetCmd{Key: "k", Value: v, Expiration: 60}).Run(g)
		},
		func(v string) []string { return []string{"transient", "set", "k", v, "60"} },
	},
}

// TestStandardValuesReachWPWhole runs value-bearing commands against a
// standard site, then runs the remote command the server received in sh with
// a fake wp, and checks wp gets each value, including an empty one, as one
// argument in its place and the shell runs nothing from it.
func TestStandardValuesReachWPWhole(t *testing.T) {
	bin := t.TempDir()
	fakeWP := "#!/bin/sh\nfor a; do printf '%s\\0' \"$a\"; done > \"$WP_ARGV\"\n"
	if err := os.WriteFile(filepath.Join(bin, "wp"), []byte(fakeWP), 0o755); err != nil {
		t.Fatal(err)
	}
	pwned := filepath.Join(t.TempDir(), "pwn")
	values := []string{"", "a b c", "it's", "x; touch " + pwned, "$(id)", "two\nlines"}
	for _, vc := range valueCases {
		for _, value := range values {
			t.Run(vc.name+"/"+value, func(t *testing.T) {
				execs := sshSite(t, "standard")
				if err := vc.run(&Globals{Site: "site", NoCache: true}, value); err != nil {
					t.Fatalf("%s %q: %v", vc.name, value, err)
				}
				got := execs()
				if len(got) != 1 {
					t.Fatalf("remote got %+v, want one command", got)
				}
				argvFile := filepath.Join(t.TempDir(), "argv")
				sh := exec.Command("sh", "-c", got[0].command)
				sh.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "WP_ARGV="+argvFile)
				if out, err := sh.CombinedOutput(); err != nil {
					t.Fatalf("sh -c %q: %v\n%s", got[0].command, err, out)
				}
				raw, err := os.ReadFile(argvFile)
				if err != nil {
					t.Fatal(err)
				}
				argv := strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
				if want := vc.argv(value); !slices.Equal(argv, want) {
					t.Fatalf("wp argv = %q, want %q (command %q)", argv, want, got[0].command)
				}
				if _, err := os.Stat(pwned); err == nil {
					t.Fatalf("the remote shell ran part of the value %q", value)
				}
			})
		}
	}
}

// wpengineValue holds every character WP Engine's gateway or the shell could
// split on or interpret.
const wpengineValue = "a b c it's \"q\" $HOME $(id) `id` x; touch /tmp/pwn | & > \\back\nline two\t"

// wpengineWrites maps each value write that runs as eval-file on WP Engine
// to the WordPress function its script calls.
var wpengineWrites = map[string]string{
	"post meta update": "update_post_meta",
	"user meta update": "update_user_meta",
	"option update":    "update_option",
	"option add":       "add_option",
}

// gatewayWords runs a value write against a WP Engine site and returns the
// words after `wp eval-file -` as WP Engine's gateway leaves them, with every
// quote dropped and split on whitespace, and the script sent on stdin.
func gatewayWords(t *testing.T, vc valueCase, value string) (words []string, script string) {
	t.Helper()
	execs := sshSite(t, "wpengine")
	if err := vc.run(&Globals{Site: "site", NoCache: true}, value); err != nil {
		t.Fatalf("%s: %v", vc.name, err)
	}
	got := execs()
	if len(got) != 1 {
		t.Fatalf("remote got %+v, want one command", got)
	}
	_, rest, ok := strings.Cut(got[0].command, " && wp eval-file - ")
	if !ok {
		t.Fatalf("command %q does not run wp eval-file -", got[0].command)
	}
	return strings.Fields(strings.NewReplacer("'", "", `"`, "").Replace(rest)), got[0].stdin
}

// TestWPEngineValueWritesSendEveryArgAfterQuoteStripping checks each value
// write on WP Engine sends one word per argument, even for an empty value,
// that decodes to the exact argument after the gateway drops quoting.
func TestWPEngineValueWritesSendEveryArgAfterQuoteStripping(t *testing.T) {
	for _, vc := range valueCases {
		call, ok := wpengineWrites[vc.name]
		if !ok {
			continue
		}
		for _, value := range []string{wpengineValue, ""} {
			t.Run(vc.name+"/"+value, func(t *testing.T) {
				words, script := gatewayWords(t, vc, value)
				decoded := make([]string, 0, len(words))
				for _, w := range words {
					enc, ok := strings.CutPrefix(w, evalArgPrefix)
					if !ok {
						t.Fatalf("word %q lacks prefix %q", w, evalArgPrefix)
					}
					b, err := base64.StdEncoding.DecodeString(enc)
					if err != nil {
						t.Fatalf("word %q is not base64: %v", w, err)
					}
					decoded = append(decoded, string(b))
				}
				if want := vc.argv(value)[len(strings.Fields(vc.name)):]; !slices.Equal(decoded, want) {
					t.Fatalf("decoded args = %q, want %q", decoded, want)
				}
				if !strings.HasPrefix(script, "<?php") || !strings.Contains(script, call+"(") {
					t.Fatalf("stdin = %q, want PHP calling %s", script, call)
				}
			})
		}
	}
}

// TestValueWritesRejectEmptyIdentity checks a value write with an empty post
// ID, user, meta key, or option name fails and sends nothing on either host
// route, since WP Engine's gateway would drop the empty word.
func TestValueWritesRejectEmptyIdentity(t *testing.T) {
	cases := map[string]func(g *Globals) error{
		"post ID":            func(g *Globals) error { return (&PostMetaUpdateCmd{ID: 0, Key: "k", Value: "v"}).Run(g) },
		"post meta key":      func(g *Globals) error { return (&PostMetaUpdateCmd{ID: 1, Key: "", Value: "v"}).Run(g) },
		"user":               func(g *Globals) error { return (&UserMetaUpdateCmd{User: "", Key: "k", Value: "v"}).Run(g) },
		"user meta key":      func(g *Globals) error { return (&UserMetaUpdateCmd{User: "admin", Key: "", Value: "v"}).Run(g) },
		"option update name": func(g *Globals) error { return (&OptionUpdateCmd{Key: "", Value: "v"}).Run(g) },
		"option add name":    func(g *Globals) error { return (&OptionAddCmd{Key: "", Value: "victim"}).Run(g) },
	}
	for name, run := range cases {
		for _, host := range []string{"standard", "wpengine"} {
			t.Run(name+"/"+host, func(t *testing.T) {
				execs := sshSite(t, host)
				if err := run(&Globals{Site: "site", NoCache: true}); err == nil {
					t.Fatal("empty identity accepted")
				}
				if got := execs(); len(got) != 0 {
					t.Fatalf("sent %+v", got)
				}
			})
		}
	}
}

// captureStderr runs f with os.Stderr redirected and returns what it wrote.
func captureStderr(t *testing.T, f func() error) (string, error) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr := os.Stderr
	os.Stderr = w
	runErr := f()
	os.Stderr = stderr
	w.Close()
	out, err := io.ReadAll(r)
	r.Close()
	if err != nil {
		t.Fatal(err)
	}
	return string(out), runErr
}

// TestValueCommandsRedactValueInOutput runs every value-bearing command with
// --dry-run and with --verbose on both host routes. Output shows the value's
// length but neither the value nor any encoding of it; dry-run sends nothing,
// and verbose still sends the real value.
func TestValueCommandsRedactValueInOutput(t *testing.T) {
	const secret = "sk_live_do not print"
	encodings := []string{
		secret,
		base64.StdEncoding.EncodeToString([]byte(secret)),
		evalArg(secret),
	}
	for _, vc := range valueCases {
		for _, host := range []string{"standard", "wpengine"} {
			for _, mode := range []string{"dry-run", "verbose"} {
				t.Run(vc.name+"/"+host+"/"+mode, func(t *testing.T) {
					execs := sshSite(t, host)
					g := &Globals{Site: "site", NoCache: true, DryRun: mode == "dry-run", Verbose: mode == "verbose"}
					out, err := captureStderr(t, func() error { return vc.run(g, secret) })
					if err != nil {
						t.Fatalf("%s: %v", vc.name, err)
					}
					for _, e := range encodings {
						if strings.Contains(out, e) {
							t.Fatalf("output %q shows %q", out, e)
						}
					}
					if !strings.Contains(out, "<value, 20 bytes>") {
						t.Fatalf("output %q lacks the redacted value", out)
					}
					got := execs()
					if mode == "dry-run" {
						if len(got) != 0 {
							t.Fatalf("dry-run sent %+v", got)
						}
						return
					}
					if len(got) != 1 || !strings.Contains(got[0].command, secret) && !strings.Contains(got[0].command, evalArg(secret)) {
						t.Fatalf("verbose sent %+v, want the value", got)
					}
				})
			}
		}
	}
}

// TestWPEngineScriptsStoreExactValue runs each eval-file script in PHP with
// the words WP Engine's gateway passes it, against stubs of WordPress that
// record writes the way WordPress stores them (meta writes unslash their
// arguments). It checks the stored key and value match the input exactly,
// and that a script given a dropped or unprefixed word stores nothing. It
// needs php on PATH.
func TestWPEngineScriptsStoreExactValue(t *testing.T) {
	php, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php not on PATH")
	}
	harness, err := filepath.Abs(filepath.Join("testdata", "eval_file_harness.php"))
	if err != nil {
		t.Fatal(err)
	}
	// runPHP runs script with args and returns the writes it recorded.
	runPHP := func(t *testing.T, script string, args []string) ([][]any, error) {
		t.Helper()
		dir := t.TempDir()
		scriptPath := filepath.Join(dir, "script.php")
		callsPath := filepath.Join(dir, "calls.json")
		if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(php, append([]string{harness, scriptPath}, args...)...)
		cmd.Env = append(os.Environ(), "CALLS_FILE="+callsPath)
		out, runErr := cmd.CombinedOutput()
		raw, err := os.ReadFile(callsPath)
		if err != nil {
			t.Fatalf("php wrote no calls: %v\n%s", err, out)
		}
		var calls [][]any
		if err := json.Unmarshal(raw, &calls); err != nil {
			t.Fatalf("calls %q: %v", raw, err)
		}
		if runErr != nil {
			runErr = fmt.Errorf("%w: %s", runErr, out)
		}
		return calls, runErr
	}
	for _, vc := range valueCases {
		call, ok := wpengineWrites[vc.name]
		if !ok {
			continue
		}
		for _, value := range []string{wpengineValue, "", `C:\path\"q"`} {
			t.Run(vc.name+"/"+value, func(t *testing.T) {
				words, script := gatewayWords(t, vc, value)
				calls, err := runPHP(t, script, words)
				if err != nil {
					t.Fatal(err)
				}
				want := []any{call, "k", value}
				if len(calls) != 1 || !slices.Equal(calls[0], want) {
					t.Fatalf("recorded %q, want one %q", calls, want)
				}
			})
		}
		t.Run(vc.name+"/bad words", func(t *testing.T) {
			words, script := gatewayWords(t, vc, "")
			for _, bad := range [][]string{words[:len(words)-1], append(slices.Clone(words), evalArg("x")), append(slices.Clone(words[:len(words)-1]), "victim")} {
				calls, err := runPHP(t, script, bad)
				if err == nil || len(calls) != 0 {
					t.Fatalf("words %q: err %v, recorded %q; want failure and no writes", bad, err, calls)
				}
			}
		})
	}
}
