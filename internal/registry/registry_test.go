package registry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSSHConfigReader(t *testing.T) {
	sshConfig := `
Host *
    ServerAliveInterval 60

Host sitealpha
    HostName 192.0.2.10
    Port 37980
    User sitealpha
    IdentityFile ~/.ssh/id_rsa

Host sitegamma
    HostName sitegamma.ssh.wpengine.net
    User sitegamma

Host devsite
    HostName dev.example.com
    User deploy
    Port 2222
`
	entries, err := ParseSSHConfigReader(strings.NewReader(sshConfig))
	if err != nil {
		t.Fatalf("ParseSSHConfigReader: %v", err)
	}

	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}

	// Verify sitealpha entry.
	rm := findEntry(entries, "sitealpha")
	if rm == nil {
		t.Fatal("sitealpha entry not found")
	}
	if rm.Hostname != "192.0.2.10" {
		t.Errorf("sitealpha hostname: got %q, want %q", rm.Hostname, "192.0.2.10")
	}
	if rm.Port != 37980 {
		t.Errorf("sitealpha port: got %d, want 37980", rm.Port)
	}
	if rm.User != "sitealpha" {
		t.Errorf("sitealpha user: got %q, want %q", rm.User, "sitealpha")
	}

	// Verify sitegamma — should have default port 22.
	ce := findEntry(entries, "sitegamma")
	if ce == nil {
		t.Fatal("sitegamma entry not found")
	}
	if ce.Port != 22 {
		t.Errorf("sitegamma port: got %d, want 22", ce.Port)
	}
	if ce.Hostname != "sitegamma.ssh.wpengine.net" {
		t.Errorf("sitegamma hostname: got %q", ce.Hostname)
	}

	// Verify devsite — custom port.
	ds := findEntry(entries, "devsite")
	if ds == nil {
		t.Fatal("devsite entry not found")
	}
	if ds.Port != 2222 {
		t.Errorf("devsite port: got %d, want 2222", ds.Port)
	}
}

func TestParseSSHConfigReader_WildcardSkip(t *testing.T) {
	sshConfig := `
Host *
    User default

Host *.example.com
    User wildcard

Host realhost
    HostName 1.2.3.4
`
	entries, err := ParseSSHConfigReader(strings.NewReader(sshConfig))
	if err != nil {
		t.Fatalf("ParseSSHConfigReader: %v", err)
	}

	// Only realhost should be present; wildcards should be skipped.
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d: %v", len(entries), entries)
	}
	if entries[0].Alias != "realhost" {
		t.Errorf("expected realhost, got %q", entries[0].Alias)
	}
}

func TestParseSSHConfigFile(t *testing.T) {
	path := filepath.Join("testdata", "ssh_config")
	entries, err := ParseSSHConfigFile(path)
	if err != nil {
		t.Fatalf("ParseSSHConfigFile: %v", err)
	}
	if len(entries) != 5 {
		t.Fatalf("expected 5 entries from fixture, got %d", len(entries))
	}
}

func TestLoadMetadata(t *testing.T) {
	path := filepath.Join("testdata", "sites.json")
	meta, err := LoadMetadata(path)
	if err != nil {
		t.Fatalf("LoadMetadata: %v", err)
	}

	if len(meta.Sites) != 3 {
		t.Fatalf("expected 3 sites, got %d", len(meta.Sites))
	}

	rm, ok := meta.Sites["sitealpha"]
	if !ok {
		t.Fatal("sitealpha not found in metadata")
	}
	if rm.WPPath != "~/public_html" {
		t.Errorf("sitealpha wp_path: got %q", rm.WPPath)
	}
	if rm.Tags["client"] != "Example Client" {
		t.Errorf("sitealpha tags: got %v", rm.Tags)
	}
}

func TestLoadMetadata_Missing(t *testing.T) {
	meta, err := LoadMetadata("/nonexistent/path/sites.json")
	if err != nil {
		t.Fatalf("expected nil error for missing file, got %v", err)
	}
	if len(meta.Sites) != 0 {
		t.Errorf("expected empty sites map, got %d", len(meta.Sites))
	}
}

func TestSaveLoadMetadata(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "sites.json")

	original := &Metadata{
		Sites: map[string]SiteOverlay{
			"testsite": {
				WPPath:   "~/www",
				HostType: "standard",
				Groups:   []string{"test"},
				Tags:     map[string]string{"env": "staging"},
			},
		},
	}

	if err := SaveMetadata(original, path); err != nil {
		t.Fatalf("SaveMetadata: %v", err)
	}

	loaded, err := LoadMetadata(path)
	if err != nil {
		t.Fatalf("LoadMetadata after save: %v", err)
	}

	if len(loaded.Sites) != 1 {
		t.Fatalf("expected 1 site, got %d", len(loaded.Sites))
	}
	site := loaded.Sites["testsite"]
	if site.WPPath != "~/www" {
		t.Errorf("wp_path: got %q", site.WPPath)
	}
	if site.Tags["env"] != "staging" {
		t.Errorf("tags: got %v", site.Tags)
	}
}

func TestSaveMetadata_AtomicWrite(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "sites.json")

	meta := &Metadata{Sites: map[string]SiteOverlay{"a": {WPPath: "~/test"}}}
	if err := SaveMetadata(meta, path); err != nil {
		t.Fatalf("SaveMetadata: %v", err)
	}

	// Ensure no .tmp file remains.
	tmpPath := path + ".tmp"
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Error("temporary file should not exist after save")
	}

	// Ensure actual file has correct content.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var m Metadata
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if m.Sites["a"].WPPath != "~/test" {
		t.Error("saved content mismatch")
	}
}

func TestNewRegistry(t *testing.T) {
	reg, err := NewRegistry(RegistryOptions{
		SSHConfigPath: filepath.Join("testdata", "ssh_config"),
		MetadataPath:  filepath.Join("testdata", "sites.json"),
		SkipDNS:       true,
	})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	// Should have 5 sites from SSH config (wildcard skipped).
	if reg.Len() != 5 {
		t.Fatalf("expected 5 sites, got %d", reg.Len())
	}

	// Verify merge: sitealpha should have WPPath from metadata.
	rm, err := reg.Get("sitealpha")
	if err != nil {
		t.Fatalf("Get sitealpha: %v", err)
	}
	if rm.WPPath != "~/public_html" {
		t.Errorf("sitealpha WPPath: got %q", rm.WPPath)
	}
	if rm.Tags["client"] != "Example Client" {
		t.Errorf("sitealpha tags: got %v", rm.Tags)
	}
}

func TestRegistry_AutoDetectWPEngine(t *testing.T) {
	reg, err := NewRegistry(RegistryOptions{
		SSHConfigPath: filepath.Join("testdata", "ssh_config"),
		MetadataPath:  filepath.Join("testdata", "sites.json"),
		SkipDNS:       true,
	})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	ce, err := reg.Get("sitegamma")
	if err != nil {
		t.Fatalf("Get sitegamma: %v", err)
	}
	if ce.HostType != "wpengine" {
		t.Errorf("sitegamma host_type: got %q, want %q", ce.HostType, "wpengine")
	}
	if ce.WPPath != "/home/wpe-user/sites/sitegamma1" {
		t.Errorf("sitegamma WPPath: got %q", ce.WPPath)
	}
}

func TestRegistry_AutoDetectStandard(t *testing.T) {
	reg, err := NewRegistry(RegistryOptions{
		SSHConfigPath: filepath.Join("testdata", "ssh_config"),
		MetadataPath:  filepath.Join("testdata", "sites.json"),
		SkipDNS:       true,
	})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	rm, err := reg.Get("sitealpha")
	if err != nil {
		t.Fatalf("Get sitealpha: %v", err)
	}
	if rm.HostType != "standard" {
		t.Errorf("sitealpha host_type: got %q, want %q", rm.HostType, "standard")
	}
}

func TestRegistry_CanonicalHostMap(t *testing.T) {
	reg, err := NewRegistry(RegistryOptions{
		SSHConfigPath: filepath.Join("testdata", "ssh_config"),
		MetadataPath:  filepath.Join("testdata", "sites.json"),
		SkipDNS:       true,
	})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	hostMap := reg.CanonicalHostMap()

	// sitealpha and sitebeta should resolve to same canonical host (IP already given).
	rmHost := hostMap["sitealpha"]
	csHost := hostMap["sitebeta"]
	if rmHost != csHost {
		t.Errorf("sitealpha (%s) and sitebeta (%s) should share canonical host", rmHost, csHost)
	}
	if rmHost != "192.0.2.10:37980" {
		t.Errorf("expected 192.0.2.10:37980, got %s", rmHost)
	}

	// sitegamma should have hostname:port (DNS skipped).
	ceHost := hostMap["sitegamma"]
	if ceHost != "sitegamma.ssh.wpengine.net:22" {
		t.Errorf("sitegamma canonical: got %s", ceHost)
	}
}

func TestRegistry_Get_NotFound(t *testing.T) {
	reg, err := NewRegistry(RegistryOptions{
		SSHConfigPath: filepath.Join("testdata", "ssh_config"),
		MetadataPath:  filepath.Join("testdata", "sites.json"),
		SkipDNS:       true,
	})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	_, err = reg.Get("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent site")
	}
}

func TestRegistry_List_Sorted(t *testing.T) {
	reg, err := NewRegistry(RegistryOptions{
		SSHConfigPath: filepath.Join("testdata", "ssh_config"),
		MetadataPath:  filepath.Join("testdata", "sites.json"),
		SkipDNS:       true,
	})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	sites := reg.List()
	for i := 1; i < len(sites); i++ {
		if sites[i-1].Alias >= sites[i].Alias {
			t.Errorf("sites not sorted: %s >= %s", sites[i-1].Alias, sites[i].Alias)
		}
	}
}

func TestRegistry_FilterByGroup(t *testing.T) {
	reg, err := NewRegistry(RegistryOptions{
		SSHConfigPath: filepath.Join("testdata", "ssh_config"),
		MetadataPath:  filepath.Join("testdata", "sites.json"),
		SkipDNS:       true,
	})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	// Filter by "client" group — sitealpha, sitebeta, sitegamma should match.
	clients := reg.Filter(FilterOptions{Group: "client"})
	if len(clients) != 3 {
		t.Errorf("expected 3 client sites, got %d", len(clients))
		for _, s := range clients {
			t.Logf("  %s", s.Alias)
		}
	}

	// Filter by "wpengine" group — only sitegamma (auto-detected).
	wpe := reg.Filter(FilterOptions{Group: "wpengine"})
	if len(wpe) != 1 {
		t.Errorf("expected 1 wpengine site, got %d", len(wpe))
	}
	if len(wpe) > 0 && wpe[0].Alias != "sitegamma" {
		t.Errorf("expected sitegamma, got %s", wpe[0].Alias)
	}

	// Filter by "all" — should return everything.
	all := reg.Filter(FilterOptions{Group: "all"})
	if len(all) != 5 {
		t.Errorf("expected 5 sites in 'all' group, got %d", len(all))
	}
}

func TestRegistry_FilterByHostType(t *testing.T) {
	reg, err := NewRegistry(RegistryOptions{
		SSHConfigPath: filepath.Join("testdata", "ssh_config"),
		MetadataPath:  filepath.Join("testdata", "sites.json"),
		SkipDNS:       true,
	})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	wpe := reg.Filter(FilterOptions{HostType: "wpengine"})
	if len(wpe) != 1 {
		t.Errorf("expected 1 wpengine site, got %d", len(wpe))
	}

	std := reg.Filter(FilterOptions{HostType: "standard"})
	if len(std) != 4 {
		t.Errorf("expected 4 standard sites, got %d", len(std))
	}
}

func TestRegistry_FilterByTag(t *testing.T) {
	reg, err := NewRegistry(RegistryOptions{
		SSHConfigPath: filepath.Join("testdata", "ssh_config"),
		MetadataPath:  filepath.Join("testdata", "sites.json"),
		SkipDNS:       true,
	})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	// Filter by tag key "client".
	tagged := reg.Filter(FilterOptions{TagKey: "client"})
	if len(tagged) != 2 {
		t.Errorf("expected 2 sites with 'client' tag, got %d", len(tagged))
	}

	// Filter by tag key + value.
	specific := reg.Filter(FilterOptions{TagKey: "client", TagValue: "Example Client"})
	if len(specific) != 1 {
		t.Errorf("expected 1 site with client=Example Client, got %d", len(specific))
	}
}

func TestRegistry_FilterCombined(t *testing.T) {
	reg, err := NewRegistry(RegistryOptions{
		SSHConfigPath: filepath.Join("testdata", "ssh_config"),
		MetadataPath:  filepath.Join("testdata", "sites.json"),
		SkipDNS:       true,
	})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	// Filter by group "client" AND host type "standard".
	result := reg.Filter(FilterOptions{Group: "client", HostType: "standard"})
	if len(result) != 2 {
		t.Errorf("expected 2 standard client sites, got %d", len(result))
	}
}

func TestMatchGroup_BuiltinPrimeVPS(t *testing.T) {
	site := &Site{
		Alias:         "test",
		Hostname:      "192.0.2.10",
		CanonicalHost: "192.0.2.10:37980",
	}
	if !MatchGroup(site, "prime-vps", nil) {
		t.Error("expected prime-vps match for 192.0.2.10")
	}
}

func TestMatchGroup_BuiltinWPEngine(t *testing.T) {
	site := &Site{
		Alias:    "test",
		Hostname: "test.ssh.wpengine.net",
		HostType: "wpengine",
	}
	if !MatchGroup(site, "wpengine", nil) {
		t.Error("expected wpengine match")
	}
}

func TestMatchGroup_UserDefined(t *testing.T) {
	site := &Site{Alias: "mysite"}
	userGroups := map[string][]string{
		"staging": {"mysite", "other"},
	}
	if !MatchGroup(site, "staging", userGroups) {
		t.Error("expected user-defined group match")
	}
	if MatchGroup(site, "production", userGroups) {
		t.Error("should not match undefined group")
	}
}

func TestMatchGroup_SiteMetadata(t *testing.T) {
	site := &Site{
		Alias:  "mysite",
		Groups: []string{"custom-group"},
	}
	if !MatchGroup(site, "custom-group", nil) {
		t.Error("expected metadata group match")
	}
}

func TestDetectHostType(t *testing.T) {
	tests := []struct {
		name     string
		site     *Site
		expected string
	}{
		{
			name:     "wpengine hostname",
			site:     &Site{Hostname: "mysite.ssh.wpengine.net", HostType: "standard"},
			expected: "wpengine",
		},
		{
			name:     "explicit wpengine",
			site:     &Site{Hostname: "1.2.3.4", HostType: "wpengine"},
			expected: "wpengine",
		},
		{
			name:     "prime vps ip",
			site:     &Site{Hostname: "192.0.2.10", HostType: "standard"},
			expected: "standard",
		},
		{
			name:     "unknown host",
			site:     &Site{Hostname: "other.example.com", HostType: "standard"},
			expected: "standard",
		},
		{
			name:     "auto detect mode",
			site:     &Site{Hostname: "test.ssh.wpengine.net", HostType: "auto"},
			expected: "wpengine",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectHostType(tt.site)
			if got != tt.expected {
				t.Errorf("detectHostType: got %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestResolveCanonicalHost(t *testing.T) {
	// IP address — should pass through.
	got := resolveCanonicalHost("192.0.2.10", 37980, false)
	if got != "192.0.2.10:37980" {
		t.Errorf("IP canonical: got %q", got)
	}

	// With DNS skip, hostname should pass through.
	got = resolveCanonicalHost("example.com", 22, true)
	if got != "example.com:22" {
		t.Errorf("skip DNS canonical: got %q", got)
	}
}

func TestRateLimitOverlay_ToRateLimitConfig(t *testing.T) {
	overlay := &RateLimitOverlay{
		DelayMs:  3000,
		MaxConns: 1,
	}
	cfg := overlay.ToRateLimitConfig()
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if cfg.Delay.Milliseconds() != 3000 {
		t.Errorf("delay: got %v, want 3s", cfg.Delay)
	}
	if cfg.MaxConns != 1 {
		t.Errorf("max_conns: got %d, want 1", cfg.MaxConns)
	}

	// Nil overlay should return nil config.
	var nilOverlay *RateLimitOverlay
	if nilOverlay.ToRateLimitConfig() != nil {
		t.Error("nil overlay should return nil config")
	}
}

func TestExpandHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine home dir")
	}

	got := expandHome("~/test/path")
	expected := filepath.Join(home, "test/path")
	if got != expected {
		t.Errorf("expandHome: got %q, want %q", got, expected)
	}

	// Non-home path should pass through.
	got = expandHome("/absolute/path")
	if got != "/absolute/path" {
		t.Errorf("expandHome absolute: got %q", got)
	}
}

func TestParseSSHConfigReader_HostSeparators(t *testing.T) {
	sshConfig := `
Host ordinary
Host=equals
Host = spaced
Host ==leading-equals
`
	entries, err := ParseSSHConfigReader(strings.NewReader(sshConfig))
	if err != nil {
		t.Fatalf("ParseSSHConfigReader: %v", err)
	}

	for _, alias := range []string{"ordinary", "equals", "spaced", "=leading-equals"} {
		if findEntry(entries, alias) == nil {
			t.Errorf("expected alias %q", alias)
		}
	}
}

func TestParseSSHConfigReader_StripsMatchExecGarbage(t *testing.T) {
	sshConfig := `
Host realhost
  HostName example.com
  User deploy
  Port 22

Match host macbook exec "nc -G 1 -z 192.168.1.52 22"
  HostName 192.168.1.52
  User localuser

Host	aftermatch
  HostName after.example.com
  User after

Match all
  User nobody

Host=equalsmatch
  HostName equals.example.com
`
	entries, err := ParseSSHConfigReader(strings.NewReader(sshConfig))
	if err != nil {
		t.Fatalf("ParseSSHConfigReader: %v", err)
	}

	if findEntry(entries, "realhost") == nil {
		t.Fatal("expected realhost entry")
	}
	if findEntry(entries, "aftermatch") == nil {
		t.Fatal("expected aftermatch entry")
	}
	if findEntry(entries, "equalsmatch") == nil {
		t.Fatal("expected equalsmatch entry")
	}

	garbage := []string{"nc", "-G", "-z", "1", "22", "192.168.1.52", "macbook"}
	for _, alias := range garbage {
		if findEntry(entries, alias) != nil {
			t.Errorf("unexpected garbage alias %q in entries", alias)
		}
	}
}

func TestIsValidSSHHostAlias(t *testing.T) {
	valid := []string{"omahadentists", "mini-ts", "github.com", "123"}
	for _, alias := range valid {
		if !isValidSSHHostAlias(alias) {
			t.Errorf("expected valid alias %q", alias)
		}
	}

	invalid := []string{"", "*", "*.example.com", "!negated", "-G", "-z", "\"nc"}
	for _, alias := range invalid {
		if isValidSSHHostAlias(alias) {
			t.Errorf("expected invalid alias %q", alias)
		}
	}
}

func findEntry(entries []SSHEntry, alias string) *SSHEntry {
	for i := range entries {
		if entries[i].Alias == alias {
			return &entries[i]
		}
	}
	return nil
}
