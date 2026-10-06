package cmd

import (
	"errors"
	"fmt"
	"strings"
)

// wpGlobalFlags are wp-cli's runtime flags, accepted before or after any
// command. --exec and --require are refused outside eval and eval-file, so
// they are not listed.
var wpGlobalFlags = map[string]bool{
	"path": true, "url": true, "ssh": true, "http": true, "user": true,
	"skip-plugins": true, "skip-themes": true, "skip-packages": true,
	"context": true, "color": true, "debug": true, "prompt": true, "quiet": true,
}

// rawDBExportFlags are the flags `wpgo raw -- db export -` accepts besides
// wpGlobalFlags. wp db export hands unknown flags to mysqldump, which also
// accepts unambiguous prefixes, so --result-file, --tab and their prefixes are
// kept out by allowing only these names. --tables and --exclude_tables are
// the only ones wp-cli splits into several mysqldump arguments; their entries
// are checked separately. --defaults is left out: it drops --no-defaults, so
// mysqldump would read option files on the server.
var rawDBExportFlags = map[string]bool{
	"tables": true, "exclude_tables": true, "include-tablespaces": true,
	"add-drop-table": true, "skip-opt": true, "where": true,
	"no-create-info": true, "create-info": true, "single-transaction": true,
	"quick": true, "lock-tables": true, "skip-lock-tables": true,
	"hex-blob": true, "routines": true, "triggers": true, "events": true,
	"default-character-set": true, "dbuser": true, "dbpass": true,
}

// wpValueFlags are the wp-cli global flags that take a value.
var wpValueFlags = map[string]bool{
	"path": true, "url": true, "ssh": true, "http": true, "user": true,
	"require": true, "exec": true, "context": true,
}

// wpFlag is one --name[=value] or -n[=value] argument as wp-cli reads it.
// name is lowercased with any "no-" prefix removed; negated records that
// prefix.
type wpFlag struct {
	name    string
	value   string
	negated bool
}

// wpReading is one way wp-cli versions may split an argv. checkWPArgv refuses
// if any reading is refused.
type wpReading struct {
	// spaceValues reads a wpValueFlags flag without "=" as taking the next
	// token as its value. wp-cli has no "--flag value" form, but a user may
	// intend it.
	spaceValues bool
	// dropDelimiters ignores every "--" token, as newer wp-cli strips them.
	// Otherwise "--" ends options and a run of "--" tokens counts as one.
	dropDelimiters bool
}

var wpReadings = []wpReading{{false, false}, {true, false}, {false, true}, {true, true}}

// parseWPArgs splits args into positionals and flags the way wp-cli's
// Configurator::extract_assoc does. A leading @alias positional is dropped,
// positionals are lowercased and the "sql" alias becomes "db".
func parseWPArgs(args []string, r wpReading) (positional []string, flags []wpFlag) {
	endOfOptions, skipNext, prevDelim := false, false, false
	for _, a := range args {
		isDelim := a == "--"
		switch {
		case skipNext:
			skipNext = false
		case isDelim && (r.dropDelimiters || prevDelim || !endOfOptions):
			endOfOptions = !r.dropDelimiters
		case endOfOptions:
			positional = append(positional, strings.ToLower(a))
		case strings.HasPrefix(a, "--") || (len(a) >= 2 && a[0] == '-' && isASCIILetter(a[1]) && (len(a) == 2 || a[2] == '=')):
			name, value, hasValue := strings.Cut(strings.TrimLeft(a, "-"), "=")
			name = strings.ToLower(name)
			negated := strings.HasPrefix(name, "no-")
			name = strings.TrimPrefix(name, "no-")
			flags = append(flags, wpFlag{name: name, value: value, negated: negated})
			skipNext = r.spaceValues && !hasValue && wpValueFlags[name]
		default:
			positional = append(positional, strings.ToLower(a))
		}
		prevDelim = isDelim
	}
	if len(positional) > 0 && strings.HasPrefix(positional[0], "@") {
		positional = positional[1:]
	}
	if len(positional) > 0 && positional[0] == "sql" {
		positional[0] = "db"
	}
	return positional, flags
}

func isASCIILetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// checkRawArgs refuses a raw invocation unless the remote shell hands wp plain
// words and none of them writes a dump or export on the server. The transport
// joins args with spaces, unquoted, so they are checked as the remote shell
// parses that string. With nested, as for WP Engine, whose gateway parses the
// command a second time, each word is parsed again and that layer is checked
// too, except for eval and eval-file, whose PHP is left to the caller.
func checkRawArgs(args []string, nested bool) error {
	line := strings.Join(args, " ")
	words, err := shellWords(line)
	if err != nil {
		return fmt.Errorf("wpgo raw refuses %q: %w; quote it in single quotes", line, err)
	}
	if err := checkWPArgv(words); err != nil {
		return err
	}
	if isDBQuery(words) {
		if err := checkSQLNoFileWrite(line); err != nil {
			return err
		}
	}
	if !nested || isEvalArgv(words) {
		return nil
	}
	var inner []string
	for _, w := range words {
		ws, err := shellWords(w)
		if err != nil {
			return fmt.Errorf("wpgo raw refuses %q on WP Engine, which parses each word again: %w", w, err)
		}
		inner = append(inner, ws...)
	}
	return checkWPArgv(inner)
}

// checkWPArgv refuses argv, the exact words wp receives, if any wpReading of
// it writes a dump or export on the server.
func checkWPArgv(argv []string) error {
	for _, r := range wpReadings {
		pos, flags := parseWPArgs(argv, r)
		if err := checkWPInvocation(pos, flags); err != nil {
			return err
		}
	}
	// wp db query hands its flags to the mysql client, so SQL can also
	// arrive in a flag value such as --execute=.
	if isDBQuery(argv) {
		return checkSQLNoFileWrite(strings.Join(argv, " "))
	}
	return nil
}

func isDBQuery(argv []string) bool {
	for _, r := range wpReadings {
		if pos, _ := parseWPArgs(argv, r); len(pos) > 1 && pos[0] == "db" && pos[1] == "query" {
			return true
		}
	}
	return false
}

// isEvalArgv reports whether every wpReading of argv runs eval or eval-file.
func isEvalArgv(argv []string) bool {
	for _, r := range wpReadings {
		if pos, _ := parseWPArgs(argv, r); len(pos) == 0 || !isEvalCommand(pos[0]) {
			return false
		}
	}
	return true
}

func isEvalCommand(cmd string) bool {
	return cmd == "eval" || cmd == "eval-file"
}

func checkWPInvocation(pos []string, flags []wpFlag) error {
	at := func(i int) string {
		if i < len(pos) {
			return pos[i]
		}
		return ""
	}
	if !isEvalCommand(at(0)) {
		for _, f := range flags {
			if f.name == "exec" || f.name == "require" {
				return fmt.Errorf("wpgo raw refuses --%s outside eval and eval-file: it runs arbitrary PHP; use `wpgo eval`", f.name)
			}
		}
	}
	switch {
	case at(0) == "export":
		return fmt.Errorf("wpgo raw refuses `wp export`: it writes WXR files on the server; nothing may be exported to the server")
	case at(0) == "db" && (at(1) == "cli" || at(1) == "connect"):
		return fmt.Errorf("wpgo raw refuses `wp db %s`: the mysql client can write files on the server; use `wpgo db query`", at(1))
	case at(0) == "db" && (at(1) == "export" || at(1) == "dump"):
		if len(pos) != 3 || pos[2] != "-" {
			return fmt.Errorf("wpgo raw refuses `wp db %s` without the single file argument '-': it writes the dump on the server; use `wpgo db export` to save it on this machine", at(1))
		}
		for _, f := range flags {
			if !wpGlobalFlags[f.name] && !rawDBExportFlags[f.name] && (f.name != "defaults" || !f.negated) {
				return fmt.Errorf("wpgo raw refuses `wp db %s` flag --%s: it may write a file on the server; use `wpgo db export`", at(1), f.name)
			}
			if f.name == "tables" || f.name == "exclude_tables" {
				for _, table := range strings.Split(f.value, ",") {
					// wp-cli trims each entry with PHP trim().
					if strings.HasPrefix(strings.Trim(table, " \t\n\r\x00\x0b"), "-") {
						return fmt.Errorf("wpgo raw refuses `wp db %s` --%s entry %q: mysqldump would read it as an option; use `wpgo db export`", at(1), f.name, table)
					}
				}
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

// shellWords splits line into the words a POSIX shell (sh, dash or bash)
// would pass to a command, handling single quotes, double quotes and
// backslash escapes. It returns an error for anything that would make the
// shell do more than pass literal words: an unquoted control operator,
// redirection, newline, comment, subshell or brace, a $ or backtick outside
// single quotes, a line continuation, or an unbalanced quote. Unquoted glob
// characters are passed through; they only match existing server files.
func shellWords(line string) ([]string, error) {
	var words []string
	var cur strings.Builder
	inWord := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case c == ' ' || c == '\t':
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
				inWord = false
			}
		case c == '\'':
			end := strings.IndexByte(line[i+1:], '\'')
			if end < 0 {
				return nil, errors.New("unbalanced single quote")
			}
			cur.WriteString(line[i+1 : i+1+end])
			i += end + 1
			inWord = true
		case c == '"':
			inWord = true
			for i++; ; i++ {
				if i >= len(line) {
					return nil, errors.New("unbalanced double quote")
				}
				c = line[i]
				if c == '"' {
					break
				}
				switch c {
				case '$', '`':
					return nil, fmt.Errorf("%c inside double quotes expands", c)
				case '\\':
					if i+1 < len(line) && line[i+1] == '\n' {
						return nil, errors.New("line continuation")
					}
					// Inside double quotes a backslash escapes only these.
					if i+1 < len(line) && strings.IndexByte("$`\"\\", line[i+1]) >= 0 {
						i++
						c = line[i]
					}
				}
				cur.WriteByte(c)
			}
		case c == '\\':
			if i+1 >= len(line) {
				return nil, errors.New("trailing backslash")
			}
			if line[i+1] == '\n' {
				return nil, errors.New("line continuation")
			}
			i++
			cur.WriteByte(line[i])
			inWord = true
		case c == '#' && !inWord:
			return nil, errors.New("unquoted # starts a comment")
		case strings.IndexByte(";&|<>()$`{}\n", c) >= 0:
			return nil, fmt.Errorf("unquoted %q is shell syntax", c)
		default:
			cur.WriteByte(c)
			inWord = true
		}
	}
	if inWord {
		words = append(words, cur.String())
	}
	return words, nil
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
