package adapter

import (
	"testing"

	"github.com/builtbyrobben/wpssh/internal/registry"
)

func TestForSite_ExplicitHostType(t *testing.T) {
	tests := []struct {
		name     string
		hostType string
		want     string
	}{
		{"explicit standard", "standard", "standard"},
		{"explicit wpengine", "wpengine", "wpengine"},
		{"explicit wpengine uppercase", "WPEngine", "wpengine"},
		{"explicit standard uppercase", "Standard", "standard"},
		{"unknown defaults to standard", "kinsta", "standard"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			site := &registry.Site{
				Hostname: "example.com",
				HostType: tt.hostType,
			}
			adapter := ForSite(site)
			if adapter.Name() != tt.want {
				t.Errorf("ForSite(hostType=%q) = %q, want %q", tt.hostType, adapter.Name(), tt.want)
			}
		})
	}
}

func TestForSite_AutoDetectFromHostname(t *testing.T) {
	tests := []struct {
		name     string
		hostname string
		want     string
	}{
		{"wpengine hostname", "sitegamma.ssh.wpengine.net", "wpengine"},
		{"wpengine hostname uppercase", "MYSITE.SSH.WPENGINE.NET", "wpengine"},
		{"standard hostname", "sitealpha.example.com", "standard"},
		{"ip address", "192.0.2.10", "standard"},
		{"cpanel hostname", "server.cpanel.net", "standard"},
		{"empty hostname", "", "standard"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			site := &registry.Site{
				Hostname: tt.hostname,
				HostType: "", // Auto-detect.
			}
			adapter := ForSite(site)
			if adapter.Name() != tt.want {
				t.Errorf("ForSite(hostname=%q) = %q, want %q", tt.hostname, adapter.Name(), tt.want)
			}
		})
	}
}

func TestForSite_ExplicitOverridesDetection(t *testing.T) {
	// Even if hostname looks like WP Engine, explicit host_type wins.
	site := &registry.Site{
		Hostname: "mysite.ssh.wpengine.net",
		HostType: "standard",
	}
	adapter := ForSite(site)
	if adapter.Name() != "standard" {
		t.Errorf("explicit standard on wpengine hostname: got %q, want standard", adapter.Name())
	}
}

func TestForSite_AutoKeyword(t *testing.T) {
	// host_type "auto" should trigger auto-detection.
	site := &registry.Site{
		Hostname: "mysite.ssh.wpengine.net",
		HostType: "auto",
	}
	adapter := ForSite(site)
	if adapter.Name() != "wpengine" {
		t.Errorf("auto on wpengine hostname: got %q, want wpengine", adapter.Name())
	}
}

func TestStandardAdapterCapabilities(t *testing.T) {
	a := &StandardAdapter{}
	caps := a.Capabilities()
	if !caps.SupportsSCP {
		t.Error("standard adapter should support SCP")
	}
	if !caps.PersistentFS {
		t.Error("standard adapter should have persistent filesystem")
	}
	if caps.MaxSessionDuration != 0 {
		t.Errorf("standard adapter session duration = %v, want 0 (no limit)", caps.MaxSessionDuration)
	}
}

func TestWPEngineAdapterCapabilities(t *testing.T) {
	a := &WPEngineAdapter{}
	caps := a.Capabilities()
	if caps.SupportsSCP {
		t.Error("wpengine adapter should NOT support SCP")
	}
	if caps.PersistentFS {
		t.Error("wpengine adapter should NOT have persistent filesystem")
	}
	if caps.MaxSessionDuration == 0 {
		t.Error("wpengine adapter should have a max session duration")
	}
}

func TestShellQuote(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"simple", "'simple'"},
		{"/home/user/public_html", "'/home/user/public_html'"},
		{"it's", "'it'\\''s'"},
		{"", "''"},
		{"path with spaces", "'path with spaces'"},
		{"$HOME/test", "'$HOME/test'"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := shellQuote(tt.input)
			if got != tt.want {
				t.Errorf("shellQuote(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestRemotePathExpr(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"~/public_html", "\"$HOME\"/'public_html'"},
		{"~", "\"$HOME\""},
		{"~/", "\"$HOME\""},
		{"~/sites/foo", "\"$HOME\"/'sites/foo'"},
		{"/home/user/public_html", "'/home/user/public_html'"},
		{"path with spaces", "'path with spaces'"},
		{"it's", "'it'\\''s'"},
		{"~/it's", "\"$HOME\"/'it'\\''s'"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := remotePathExpr(tt.input)
			if got != tt.want {
				t.Errorf("remotePathExpr(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
