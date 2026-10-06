package scripts

import (
	"embed"
	"fmt"
)

//go:embed scripts/*.sh
var scriptFS embed.FS

// GetScript returns the contents of an embedded script by name.
// Name should be the filename without the "scripts/" prefix
// (e.g., "health-check.sh").
func GetScript(name string) (string, error) {
	data, err := scriptFS.ReadFile("scripts/" + name)
	if err != nil {
		return "", fmt.Errorf("embedded script %q not found: %w", name, err)
	}
	return string(data), nil
}

// MustGetScript returns the script contents or panics.
// Use only during init or for known-good script names.
func MustGetScript(name string) string {
	s, err := GetScript(name)
	if err != nil {
		panic(err)
	}
	return s
}

// Available script names.
const (
	ScriptHealthCheck   = "health-check.sh"
	ScriptCacheClear    = "cache-clear.sh"
	ScriptSecurityAudit = "security-audit.sh"
)
