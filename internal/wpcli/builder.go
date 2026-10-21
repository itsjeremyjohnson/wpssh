package wpcli

import (
	"fmt"
	"sort"
	"strings"
)

// Word is a fixed wp-cli command word, such as "post" or "update". Only an
// untyped string constant converts to Word implicitly, so a user value cannot
// become a command word by accident: pass user values with Arg, which escapes
// them. Build writes words unescaped.
type Word string

// Command builds a wp-cli command string with proper shell escaping.
type Command struct {
	parts     []Word
	flags     map[string]string
	boolFlags []string
	args      []arg
	format    string
}

// arg is a positional argument. Build writes value escaped; Display writes
// shown instead when it is set.
type arg struct {
	value string
	shown string
}

// New creates a new wp-cli command builder for the fixed command words.
func New(parts ...Word) *Command {
	return &Command{
		parts: parts,
		flags: make(map[string]string),
	}
}

// Flag adds --key=value to the command.
func (c *Command) Flag(key, value string) *Command {
	c.flags[key] = value
	return c
}

// BoolFlag adds --key (no value) to the command.
func (c *Command) BoolFlag(key string) *Command {
	c.boolFlags = append(c.boolFlags, key)
	return c
}

// Format sets --format=X (json for machine parsing, table for display).
func (c *Command) Format(f string) *Command {
	c.format = f
	return c
}

// Arg adds a positional argument, including an empty one.
func (c *Command) Arg(value string) *Command {
	c.args = append(c.args, arg{value: value})
	return c
}

// ValueArg adds a positional argument that may hold a secret, such as an
// option or meta value. Display shows Redact(value) in its place.
func (c *Command) ValueArg(value string) *Command {
	return c.ArgShown(value, Redact(value))
}

// ArgShown adds a positional argument that Display shows as shown.
func (c *Command) ArgShown(value, shown string) *Command {
	c.args = append(c.args, arg{value: value, shown: shown})
	return c
}

// Redact is how verbose and dry-run output show a value that may be secret:
// its length only.
func Redact(value string) string {
	return fmt.Sprintf("<value, %d bytes>", len(value))
}

// Build returns the full command string: "cd {wpPath} && wp {parts} {args} {flags}"
// Path expressions preserve remote ~/ expansion; other values are single-quoted.
func (c *Command) Build(wpPath string) string {
	return c.build(wpPath, false)
}

// Display returns the command as Build does, with each ValueArg or ArgShown
// argument replaced by its shown text, for verbose and dry-run output.
func (c *Command) Display(wpPath string) string {
	return c.build(wpPath, true)
}

func (c *Command) build(wpPath string, display bool) string {
	var b strings.Builder

	b.WriteString("cd ")
	b.WriteString(pathEscape(wpPath))
	b.WriteString(" && wp")

	for _, p := range c.parts {
		b.WriteByte(' ')
		b.WriteString(string(p))
	}

	for _, a := range c.args {
		b.WriteByte(' ')
		if display && a.shown != "" {
			b.WriteString(a.shown)
		} else {
			b.WriteString(shellEscape(a.value))
		}
	}

	if c.format != "" {
		b.WriteString(" --format=")
		b.WriteString(shellEscape(c.format))
	}

	// Sort flags for deterministic output
	keys := make([]string, 0, len(c.flags))
	for k := range c.flags {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		b.WriteString(" --")
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(shellEscape(c.flags[k]))
	}

	// Sort bool flags for deterministic output
	sorted := make([]string, len(c.boolFlags))
	copy(sorted, c.boolFlags)
	sort.Strings(sorted)

	for _, k := range sorted {
		b.WriteString(" --")
		b.WriteString(k)
	}

	return b.String()
}

// Name returns the command words, e.g. "post meta update", without arguments
// or flags.
func (c *Command) Name() string {
	words := make([]string, len(c.parts))
	for i, p := range c.parts {
		words[i] = string(p)
	}
	return strings.Join(words, " ")
}

// CacheKey returns the normalized form for cache lookup:
// "command:sorted_flags" e.g., "plugin list:--format=json --status=active"
func (c *Command) CacheKey() string {
	var parts []string

	// Command parts
	cmdPart := c.Name()

	// Collect all flags into a sorted list
	var flagParts []string

	if c.format != "" {
		flagParts = append(flagParts, fmt.Sprintf("--format=%s", c.format))
	}

	for k, v := range c.flags {
		flagParts = append(flagParts, fmt.Sprintf("--%s=%s", k, v))
	}

	for _, k := range c.boolFlags {
		flagParts = append(flagParts, fmt.Sprintf("--%s", k))
	}

	sort.Strings(flagParts)

	parts = append(parts, cmdPart)
	if len(flagParts) > 0 {
		parts = append(parts, strings.Join(flagParts, " "))
	}

	return strings.Join(parts, ":")
}

// pathEscape returns a shell-safe remote path expression.
// Leading ~/ becomes "$HOME"/'…' so tilde expansion still works remotely.
func pathEscape(path string) string {
	if path == "~" || path == "~/" {
		return "\"$HOME\""
	}
	if strings.HasPrefix(path, "~/") {
		return "\"$HOME\"/" + shellEscape(strings.TrimPrefix(path, "~/"))
	}
	return shellEscape(path)
}

// shellEscape wraps a value in single quotes, escaping any single quotes
// within the value. This prevents shell interpolation attacks.
func shellEscape(s string) string {
	// Replace ' with '\'' (end quote, escaped quote, start quote)
	escaped := strings.ReplaceAll(s, "'", `'\''`)
	return "'" + escaped + "'"
}
