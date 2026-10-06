package scripts

import (
	"strings"
	"testing"
)

func TestGetScript_HealthCheck(t *testing.T) {
	s, err := GetScript(ScriptHealthCheck)
	if err != nil {
		t.Fatalf("GetScript(%s): %v", ScriptHealthCheck, err)
	}
	if !strings.Contains(s, "#!/bin/bash") {
		t.Error("health-check.sh should start with shebang")
	}
	if !strings.Contains(s, "wp core version") {
		t.Error("health-check.sh should contain 'wp core version'")
	}
	if !strings.Contains(s, "php_version") {
		t.Error("health-check.sh should collect PHP version")
	}
}

func TestGetScript_CacheClear(t *testing.T) {
	s, err := GetScript(ScriptCacheClear)
	if err != nil {
		t.Fatalf("GetScript(%s): %v", ScriptCacheClear, err)
	}
	if !strings.Contains(s, "wp cache flush") {
		t.Error("cache-clear.sh should contain 'wp cache flush'")
	}
	if !strings.Contains(s, "litespeed") {
		t.Error("cache-clear.sh should check for LiteSpeed")
	}
	if !strings.Contains(s, "wp-rocket") {
		t.Error("cache-clear.sh should check for WP Rocket")
	}
}

func TestGetScript_SecurityAudit(t *testing.T) {
	s, err := GetScript(ScriptSecurityAudit)
	if err != nil {
		t.Fatalf("GetScript(%s): %v", ScriptSecurityAudit, err)
	}
	if !strings.Contains(s, "wp core verify-checksums") {
		t.Error("security-audit.sh should verify checksums")
	}
	if !strings.Contains(s, "WP_DEBUG") {
		t.Error("security-audit.sh should check WP_DEBUG")
	}
}

func TestGetScript_NotFound(t *testing.T) {
	_, err := GetScript("nonexistent.sh")
	if err == nil {
		t.Error("expected error for nonexistent script")
	}
}

func TestMustGetScript_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("MustGetScript should panic for nonexistent script")
		}
	}()
	MustGetScript("nonexistent.sh")
}

func TestMustGetScript_Success(t *testing.T) {
	s := MustGetScript(ScriptHealthCheck)
	if s == "" {
		t.Error("MustGetScript should return non-empty for valid script")
	}
}

func TestAllScriptsOutputJSON(t *testing.T) {
	scriptNames := []string{
		ScriptHealthCheck,
		ScriptCacheClear,
		ScriptSecurityAudit,
	}
	for _, name := range scriptNames {
		t.Run(name, func(t *testing.T) {
			s, err := GetScript(name)
			if err != nil {
				t.Fatalf("GetScript(%s): %v", name, err)
			}
			// All scripts should output JSON (contain at least one JSON block).
			if !strings.Contains(s, "ENDJSON") {
				t.Errorf("%s should use heredoc JSON output (ENDJSON marker)", name)
			}
		})
	}
}
