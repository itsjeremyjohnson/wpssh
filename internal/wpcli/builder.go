package wpcli

import (
	"fmt"
	"sort"
	"strings"
)

// Command builds a wp-cli command string with proper shell escaping.
type Command struct {
	parts     []string
	flags     map[string]string
	boolFlags []string
	args      []string
	format    string
}

// New creates a new wp-cli command builder.
func New(parts ...string) *Command {
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

// Arg adds a positional argument.
func (c *Command) Arg(value string) *Command {
	c.args = append(c.args, value)
	return c
}

// Build returns the full command string: "cd {wpPath} && wp {parts} {args} {flags}"
// Path expressions preserve remote ~/ expansion; other values are single-quoted.
func (c *Command) Build(wpPath string) string {
	var b strings.Builder

	b.WriteString("cd ")
	b.WriteString(pathEscape(wpPath))
	b.WriteString(" && wp")

	for _, p := range c.parts {
		b.WriteByte(' ')
		b.WriteString(p)
	}

	for _, a := range c.args {
		b.WriteByte(' ')
		b.WriteString(shellEscape(a))
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

// CacheKey returns the normalized form for cache lookup:
// "command:sorted_flags" e.g., "plugin list:--format=json --status=active"
func (c *Command) CacheKey() string {
	var parts []string

	// Command parts
	cmdPart := strings.Join(c.parts, " ")

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
