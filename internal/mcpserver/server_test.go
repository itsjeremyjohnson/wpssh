package mcpserver

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/builtbyrobben/wpssh/internal/wpcli"
)

type fakeService struct {
	site    string
	command string
}

func (f *fakeService) ListSites() []Site {
	return []Site{{Alias: "client", HostType: "standard", Groups: []string{"production"}}}
}

func (f *fakeService) Run(_ context.Context, site string, command *wpcli.Command) (string, error) {
	f.site, f.command = site, command.Build("/var/www/html")
	if site == "" {
		return "", context.Canceled
	}
	if strings.Contains(f.command, "plugin list") {
		return `[{"name":"akismet","status":"active","version":"5.3"}]`, nil
	}
	if strings.Contains(f.command, "option get") {
		return "  keep spaces  \n", nil
	}
	return "Success", nil
}

func (*fakeService) Close() error { return nil }

type bearerTransport struct {
	token string
	base  http.RoundTripper
}

type publicHostTransport struct{ base http.RoundTripper }

func (t publicHostTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Host = "mcp.example.com"
	resp, err := t.base.RoundTrip(r)
	if err != nil {
		return nil, fmt.Errorf("send proxy test request: %w", err)
	}
	return resp, nil
}

func (t bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+t.token)
	resp, err := t.base.RoundTrip(r)
	if err != nil {
		return nil, fmt.Errorf("send test HTTP request: %w", err)
	}
	return resp, nil
}

func TestHTTPDiscoveryAndCalls(t *testing.T) {
	const token = "12345678901234567890123456789012"
	service := &fakeService{}
	server := httptest.NewServer(BearerAuth(token, Handler(service, false, "0.1.0")))
	defer server.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	httpClient := &http.Client{Transport: bearerTransport{token: token, base: http.DefaultTransport}}
	for range 2 {
		session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: server.URL, HTTPClient: httpClient}, nil)
		if err != nil {
			t.Fatalf("connect: %v", err)
		}
		if got := session.InitializeResult().ProtocolVersion; got != "2026-07-28" {
			t.Fatalf("protocol version = %q, want 2026-07-28", got)
		}
		if info := session.InitializeResult().ServerInfo; info == nil || info.Version != "0.1.0" {
			t.Fatalf("server version = %+v, want 0.1.0", info)
		}
		names := make([]string, 0, 7)
		for tool, err := range session.Tools(context.Background(), nil) {
			if err != nil {
				t.Fatal(err)
			}
			names = append(names, tool.Name)
		}
		if len(names) != 7 || contains(names, "activate_plugin") {
			t.Fatalf("read-only catalog = %v", names)
		}
		result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
			Name: "list_plugins", Arguments: map[string]any{"site": "client"},
		})
		if err != nil || result.IsError {
			t.Fatalf("list_plugins: result=%+v err=%v", result, err)
		}
		if service.site != "client" || !strings.Contains(service.command, "wp plugin list --format='json'") {
			t.Fatalf("wrong execution: site=%q command=%q", service.site, service.command)
		}
		if !strings.Contains(result.Content[0].(*mcp.TextContent).Text, "akismet") {
			t.Fatalf("unexpected result: %+v", result)
		}
		result, err = session.CallTool(context.Background(), &mcp.CallToolParams{Name: "get_option", Arguments: map[string]any{"site": "client", "key": "tagline"}})
		if err != nil || result.IsError || !strings.Contains(result.Content[0].(*mcp.TextContent).Text, "  keep spaces  ") {
			t.Fatalf("option whitespace changed: result=%+v err=%v", result, err)
		}
		result, err = session.CallTool(context.Background(), &mcp.CallToolParams{Name: "activate_plugin", Arguments: map[string]any{"site": "client", "plugin": "akismet"}})
		if err == nil && !result.IsError {
			t.Fatal("write tool available in read-only mode")
		}
		if err := session.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestHTTPAuthentication(t *testing.T) {
	const token = "12345678901234567890123456789012"
	service := &fakeService{}
	server := httptest.NewServer(BearerAuth(token, Handler(service, true, "0.1.0")))
	defer server.Close()
	for _, provided := range []string{"", "Bearer wrong"} {
		req, err := http.NewRequest(http.MethodPost, server.URL, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"server/discover"}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", provided)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized || service.command != "" {
			t.Fatalf("auth %q: status=%d command=%q", provided, resp.StatusCode, service.command)
		}
	}
}

func TestWriteToolUsesExplicitSiteAndEscapesValue(t *testing.T) {
	const token = "12345678901234567890123456789012"
	service := &fakeService{}
	server := httptest.NewServer(BearerAuth(token, Handler(service, true, "0.1.0")))
	defer server.Close()
	httpClient := &http.Client{Transport: bearerTransport{token: token, base: http.DefaultTransport}}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: server.URL, HTTPClient: httpClient}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "update_option", Arguments: map[string]any{
		"site": "client", "key": "blogdescription", "value": "it's live; touch /tmp/bad",
	}})
	if err != nil || result.IsError {
		t.Fatalf("update_option: result=%+v err=%v", result, err)
	}
	if service.site != "client" || !strings.Contains(service.command, `'it'\''s live; touch /tmp/bad'`) {
		t.Fatalf("unsafe command: site=%q command=%q", service.site, service.command)
	}
	result, err = session.CallTool(context.Background(), &mcp.CallToolParams{Name: "update_option", Arguments: map[string]any{
		"site": "client", "key": "posts_per_page", "value": "-1",
	}})
	if err != nil || result.IsError || !strings.Contains(service.command, "'-1'") {
		t.Fatalf("negative option value rejected: result=%+v err=%v command=%q", result, err, service.command)
	}
}

func TestLoopbackProxyAcceptsPublicHost(t *testing.T) {
	const token = "12345678901234567890123456789012"
	server := httptest.NewServer(BearerAuth(token, Handler(&fakeService{}, false, "0.1.0")))
	defer server.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	httpClient := &http.Client{Transport: bearerTransport{token: token, base: publicHostTransport{base: http.DefaultTransport}}}
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: server.URL, HTTPClient: httpClient}, nil)
	if err != nil {
		t.Fatalf("connect through host-preserving proxy: %v", err)
	}
	defer session.Close()
}

func TestToolArgumentsRejectWPCLIFlags(t *testing.T) {
	const token = "12345678901234567890123456789012"
	service := &fakeService{}
	server := httptest.NewServer(BearerAuth(token, Handler(service, true, "0.1.0")))
	defer server.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	httpClient := &http.Client{Transport: bearerTransport{token: token, base: http.DefaultTransport}}
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: server.URL, HTTPClient: httpClient}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"get_option", map[string]any{"site": "client", "key": "--exec=system('id');"}},
		{"activate_plugin", map[string]any{"site": "client", "plugin": "--require=/tmp/evil.php"}},
		{"update_option", map[string]any{"site": "client", "key": "blogname", "value": " --exec=system('id');"}},
	} {
		result, callErr := session.CallTool(context.Background(), &mcp.CallToolParams{Name: tc.name, Arguments: tc.args})
		if callErr == nil && !result.IsError {
			t.Errorf("%s accepted a WP-CLI flag: %+v", tc.name, result)
		}
		if service.command != "" {
			t.Fatalf("%s reached SSH execution: %q", tc.name, service.command)
		}
	}
}

func TestHTTPSAndOriginProtection(t *testing.T) {
	const token = "12345678901234567890123456789012"
	service := &fakeService{}
	server := httptest.NewTLSServer(BearerAuth(token, Handler(service, false, "0.1.0")))
	defer server.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	httpClient := server.Client()
	httpClient.Transport = bearerTransport{token: token, base: httpClient.Transport}
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: server.URL, HTTPClient: httpClient}, nil)
	if err != nil {
		t.Fatalf("HTTPS connect: %v", err)
	}
	defer session.Close()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "list_sites", Arguments: map[string]any{}})
	if err != nil || result.IsError || !strings.Contains(result.Content[0].(*mcp.TextContent).Text, "client") {
		t.Fatalf("HTTPS call: result=%+v err=%v", result, err)
	}

	req, err := http.NewRequest(http.MethodPost, server.URL, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"server/discover"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Origin", "https://attacker.example")
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin request status=%d, want 403", resp.StatusCode)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
