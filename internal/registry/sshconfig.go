package registry

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	ssh_config "github.com/kevinburke/ssh_config"
)

// SSHEntry represents a single host entry parsed from an SSH config file.
type SSHEntry struct {
	Alias        string
	Hostname     string
	Port         int
	User         string
	IdentityFile string
}

// ParseSSHConfig reads and parses the user's ~/.ssh/config file.
func ParseSSHConfig() ([]SSHEntry, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(home, ".ssh", "config")
	return ParseSSHConfigFile(path)
}

// ParseSSHConfigFile parses an SSH config from the given file path.
func ParseSSHConfigFile(path string) ([]SSHEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ParseSSHConfigReader(f)
}

// ParseSSHConfigReader parses an SSH config from an io.Reader.
func ParseSSHConfigReader(r io.Reader) ([]SSHEntry, error) {
	// kevinburke/ssh_config mis-parses Match exec lines into fake Host
	// aliases (e.g. "nc, -G, -z, ports). Strip Match blocks first.
	filtered, err := stripSSHMatchBlocks(r)
	if err != nil {
		return nil, err
	}
	cfg, err := ssh_config.Decode(filtered)
	if err != nil {
		return nil, err
	}
	return extractEntries(cfg)
}

func extractEntries(cfg *ssh_config.Config) ([]SSHEntry, error) {
	var entries []SSHEntry

	for _, host := range cfg.Hosts {
		for _, pattern := range host.Patterns {
			alias := pattern.String()

			// Skip wildcards, negated patterns, and Match-exec garbage.
			if !isValidSSHHostAlias(alias) {
				continue
			}

			entry := SSHEntry{
				Alias: alias,
				Port:  22, // default
			}

			// Extract key-value nodes from this host block.
			for _, node := range host.Nodes {
				kv, ok := node.(*ssh_config.KV)
				if !ok {
					continue
				}
				switch strings.ToLower(kv.Key) {
				case "hostname":
					entry.Hostname = kv.Value
				case "port":
					if p, err := strconv.Atoi(kv.Value); err == nil {
						entry.Port = p
					}
				case "user":
					entry.User = kv.Value
				case "identityfile":
					entry.IdentityFile = expandHome(kv.Value)
				}
			}

			// If no explicit Hostname, the alias itself is the hostname.
			if entry.Hostname == "" {
				entry.Hostname = alias
			}

			entries = append(entries, entry)
		}
	}
	return entries, nil
}

// stripSSHMatchBlocks removes OpenSSH Match blocks so they are not
// misinterpreted as Host entries by the SSH config parser.
func stripSSHMatchBlocks(r io.Reader) (io.Reader, error) {
	var b strings.Builder
	scanner := bufio.NewScanner(r)
	// Match lines can be long; raise the default token limit.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	inMatch := false
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)

		if strings.HasPrefix(lower, "match ") || lower == "match" {
			inMatch = true
			continue
		}
		if inMatch {
			// A new Host starts a real host block and ends Match scope.
			if strings.HasPrefix(lower, "host ") || lower == "host" {
				inMatch = false
			} else {
				// Still inside Match body (keywords, blanks, comments).
				continue
			}
		}
		if !inMatch {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return strings.NewReader(b.String()), nil
}

// isValidSSHHostAlias rejects wildcards and garbage tokens leaked from Match exec lines.
func isValidSSHHostAlias(alias string) bool {
	if alias == "" || alias == "*" {
		return false
	}
	if strings.ContainsAny(alias, "*?") || strings.HasPrefix(alias, "!") {
		return false
	}
	// Flags / quoted fragments from Match exec "nc -G 1 -z host port"
	if strings.HasPrefix(alias, "-") || strings.Contains(alias, "\"") {
		return false
	}
	// Bare ports are not host aliases.
	if _, err := strconv.Atoi(alias); err == nil {
		return false
	}
	return true
}

// expandHome replaces ~ prefix with the user's home directory.
func expandHome(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[1:])
}
