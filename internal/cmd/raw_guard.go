package cmd

import (
	"fmt"
	"strings"
)

// wpGlobalFlags are wp-cli's runtime flags, accepted before or after any
// command.
var wpGlobalFlags = map[string]bool{
	"path": true, "url": true, "ssh": true, "http": true, "user": true,
	"skip-plugins": true, "skip-themes": true, "skip-packages": true,
	"require": true, "exec": true, "context": true, "color": true,
	"debug": true, "prompt": true, "quiet": true,
}

// rawDBExportFlags are the flags `wpgo raw -- db export -` accepts besides
// wpGlobalFlags. wp db export hands unknown flags to mysqldump, which also
// accepts unambiguous prefixes, so --result-file, --tab and their prefixes are
// kept out by allowing only these names.
var rawDBExportFlags = map[string]bool{
	"tables": true, "exclude_tables": true, "include-tablespaces": true,
	"add-drop-table": true, "skip-opt": true, "where": true,
	"no-create-info": true, "create-info": true, "single-transaction": true,
	"quick": true, "lock-tables": true, "skip-lock-tables": true,
	"hex-blob": true, "routines": true, "triggers": true, "events": true,
	"default-character-set": true, "dbuser": true, "dbpass": true,
	"defaults": true,
}

// wpValueFlags are the wpGlobalFlags that take a value.
var wpValueFlags = map[string]bool{
	"path": true, "url": true, "ssh": true, "http": true, "user": true,
	"require": true, "exec": true, "context": true,
}

// wpFlag is one --name[=value] or -n[=value] argument as wp-cli reads it.
// name is lowercased with any "no-" prefix removed.
type wpFlag struct {
	name  string
	value string
}

// parseWPArgs splits args into positionals and flags the way wp-cli's
// Configurator::extract_assoc does: everything after "--" is positional, and
// wp-cli has no "--flag value" form, so a token after a flag is positional.
// With spaceValues, a wpValueFlags flag without "=" instead takes the next
// token as its value, the reading a user may intend. A leading @alias
// positional is dropped. Positionals are lowercased.
func parseWPArgs(args []string, spaceValues bool) (positional []string, flags []wpFlag) {
	endOfOptions, skipNext := false, false
	for _, a := range args {
		switch {
		case skipNext:
			skipNext = false
		case endOfOptions:
			positional = append(positional, strings.ToLower(a))
		case a == "--":
			endOfOptions = true
		case strings.HasPrefix(a, "--") || (len(a) >= 2 && a[0] == '-' && isASCIILetter(a[1]) && (len(a) == 2 || a[2] == '=')):
			name, value, hasValue := strings.Cut(strings.TrimLeft(a, "-"), "=")
			name = strings.TrimPrefix(strings.ToLower(name), "no-")
			flags = append(flags, wpFlag{name: name, value: value})
			skipNext = spaceValues && !hasValue && wpValueFlags[name]
		default:
			positional = append(positional, strings.ToLower(a))
		}
	}
	if len(positional) > 0 && strings.HasPrefix(positional[0], "@") {
		positional = positional[1:]
	}
	return positional, flags
}

func isASCIILetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// checkRawArgs refuses wp-cli invocations that write a dump or export on the
// server. args are the exact argv passed to wp. It refuses if either reading
// of "--flag value" is refused.
func checkRawArgs(args []string) error {
	for _, spaceValues := range []bool{false, true} {
		pos, flags := parseWPArgs(args, spaceValues)
		if err := checkWPInvocation(pos, flags); err != nil {
			return err
		}
		// wp db query hands its flags to the mysql client, so SQL can also
		// arrive in a flag value such as --execute=.
		if len(pos) > 1 && pos[0] == "db" && pos[1] == "query" {
			if err := checkSQLNoFileWrite(strings.Join(args, " ")); err != nil {
				return err
			}
		}
	}
	return nil
}

func checkWPInvocation(pos []string, flags []wpFlag) error {
	at := func(i int) string {
		if i < len(pos) {
			return pos[i]
		}
		return ""
	}
	switch {
	case at(0) == "export":
		return fmt.Errorf("wpgo raw refuses `wp export`: it writes WXR files on the server; nothing may be exported to the server")
	case at(0) == "db" && (at(1) == "export" || at(1) == "dump"):
		if len(pos) != 3 || pos[2] != "-" {
			return fmt.Errorf("wpgo raw refuses `wp db %s` without the single file argument '-': it writes the dump on the server; use `wpgo db export` to save it on this machine", at(1))
		}
		for _, f := range flags {
			if !wpGlobalFlags[f.name] && !rawDBExportFlags[f.name] {
				return fmt.Errorf("wpgo raw refuses `wp db %s` flag --%s: it may write a file on the server; use `wpgo db export`", at(1), f.name)
			}
		}
	case at(0) == "search-replace" || (at(0) == "db" && at(1) == "search-replace"):
		for _, f := range flags {
			if f.name == "export" {
				return fmt.Errorf("wpgo raw refuses search-replace with --export: it writes a SQL dump on the server; run search-replace without --export and take backups with `wpgo db export`")
			}
		}
	}
	return nil
}

// checkSQLNoFileWrite refuses SQL that mentions OUTFILE or DUMPFILE in any
// case. It matches the bare word so comments or versioned comments between
// INTO and the keyword cannot hide it.
func checkSQLNoFileWrite(sql string) error {
	upper := strings.ToUpper(sql)
	for _, kw := range []string{"OUTFILE", "DUMPFILE"} {
		if strings.Contains(upper, kw) {
			return fmt.Errorf("refusing SQL containing %s: SELECT ... INTO %s writes a file on the database server; take backups with `wpgo db export`", kw, kw)
		}
	}
	return nil
}
