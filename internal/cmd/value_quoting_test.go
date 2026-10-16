package cmd

import (
	"encoding/base64"
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
}

// TestStandardValuesReachWPWhole runs value-bearing commands against a
// standard site, then runs the remote command the server received in sh with
// a fake wp, and checks wp gets each value as one argument and the shell
// runs nothing from it.
func TestStandardValuesReachWPWhole(t *testing.T) {
	bin := t.TempDir()
	fakeWP := "#!/bin/sh\nfor a; do printf '%s\\0' \"$a\"; done > \"$WP_ARGV\"\n"
	if err := os.WriteFile(filepath.Join(bin, "wp"), []byte(fakeWP), 0o755); err != nil {
		t.Fatal(err)
	}
	pwned := filepath.Join(t.TempDir(), "pwn")
	values := []string{"a b c", "it's", "x; touch " + pwned, "$(id)", "two\nlines"}
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

// TestWPEngineValueWritesStoreExactValue runs the value writes against a WP
// Engine site and checks each sends PHP calling the matching WordPress
// function on stdin to `wp eval-file -`, with arguments that decode to the
// exact values after the gateway drops quoting.
func TestWPEngineValueWritesStoreExactValue(t *testing.T) {
	calls := map[string]string{
		"post meta update": "update_post_meta(",
		"user meta update": "update_user_meta(",
		"option update":    "update_option(",
		"option add":       "add_option(",
	}
	for _, vc := range valueCases {
		call, ok := calls[vc.name]
		if !ok {
			continue
		}
		t.Run(vc.name, func(t *testing.T) {
			execs := sshSite(t, "wpengine")
			if err := vc.run(&Globals{Site: "site", NoCache: true}, wpengineValue); err != nil {
				t.Fatalf("%s: %v", vc.name, err)
			}
			got := execs()
			if len(got) != 1 {
				t.Fatalf("remote got %+v, want one command", got)
			}
			_, words, ok := strings.Cut(got[0].command, " && wp eval-file - ")
			if !ok {
				t.Fatalf("command %q does not run wp eval-file -", got[0].command)
			}
			fields := strings.Fields(strings.ReplaceAll(words, "'", ""))
			decoded := make([]string, 0, len(fields))
			for _, w := range fields {
				b, err := base64.StdEncoding.DecodeString(w)
				if err != nil {
					t.Fatalf("argument %q is not base64: %v", w, err)
				}
				decoded = append(decoded, string(b))
			}
			if want := vc.argv(wpengineValue)[len(strings.Fields(vc.name)):]; !slices.Equal(decoded, want) {
				t.Fatalf("decoded args = %q, want %q", decoded, want)
			}
			if !strings.HasPrefix(got[0].stdin, "<?php") || !strings.Contains(got[0].stdin, call) {
				t.Fatalf("stdin = %q, want PHP calling %s", got[0].stdin, call)
			}
		})
	}
}

// TestWPEngineValueWriteDryRunHidesValue checks dry-run on WP Engine sends
// nothing and prints the command without the value or its encoding.
func TestWPEngineValueWriteDryRunHidesValue(t *testing.T) {
	const secret = "sk_live_do not print"
	execs := sshSite(t, "wpengine")
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr := os.Stderr
	os.Stderr = w
	runErr := (&OptionUpdateCmd{Key: "api_key", Value: secret}).Run(&Globals{Site: "site", NoCache: true, DryRun: true})
	os.Stderr = stderr
	w.Close()
	out, err := io.ReadAll(r)
	r.Close()
	if err != nil {
		t.Fatal(err)
	}
	if runErr != nil {
		t.Fatalf("dry-run option update: %v", runErr)
	}
	if got := execs(); len(got) != 0 {
		t.Fatalf("dry-run sent %+v", got)
	}
	shown := string(out)
	encoded := base64.StdEncoding.EncodeToString([]byte(secret))
	if strings.Contains(shown, secret) || strings.Contains(shown, encoded) ||
		!strings.Contains(shown, `wp eval-file - <base64 "api_key"> <base64 value, 20 bytes> < [PHP calling update_option]`) {
		t.Fatalf("dry-run printed %q", shown)
	}
}
