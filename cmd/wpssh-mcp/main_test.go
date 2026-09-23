package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadTokenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("file-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WPSMCP_TOKEN", "environment-secret")
	t.Setenv("WPSMCP_TOKEN_FILE", path)
	token, err := loadToken()
	if err != nil || token != "file-secret" {
		t.Fatalf("loadToken() = %q, %v", token, err)
	}
}

func TestLoopbackAddress(t *testing.T) {
	for _, tc := range []struct {
		address string
		want    bool
	}{
		{"127.0.0.1:8080", true},
		{"[::1]:8080", true},
		{"0.0.0.0:8080", false},
		{"192.0.2.1:8080", false},
		{"localhost:8080", false},
		{"invalid", false},
	} {
		if got := loopbackAddress(tc.address); got != tc.want {
			t.Errorf("loopbackAddress(%q) = %v, want %v", tc.address, got, tc.want)
		}
	}
}

func TestRunRejectsPublicPlainHTTP(t *testing.T) {
	t.Setenv("WPSMCP_TOKEN", "12345678901234567890123456789012")
	t.Setenv("WPSMCP_LISTEN_ADDR", "0.0.0.0:8080")
	t.Setenv("WPSMCP_TLS_CERT_FILE", "")
	t.Setenv("WPSMCP_TLS_KEY_FILE", "")
	t.Setenv("WPSMCP_BEHIND_PROXY", "")
	if err := run(); !errors.Is(err, errPublicHTTP) {
		t.Fatalf("run() = %v, want public HTTP rejection", err)
	}
}
