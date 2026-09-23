package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/builtbyrobben/wpssh/internal/wpcli"
)

var (
	errKeyRequired    = errors.New("key is required")
	errPluginRequired = errors.New("plugin is required")
	errUnsafeArgument = errors.New("argument cannot begin with a hyphen")
)

type siteInput struct {
	Site string `json:"site" jsonschema:"registered SSH site alias"`
}

type optionInput struct {
	Site string `json:"site" jsonschema:"registered SSH site alias"`
	Key  string `json:"key" jsonschema:"WordPress option name"`
}

type pluginInput struct {
	Site   string `json:"site" jsonschema:"registered SSH site alias"`
	Plugin string `json:"plugin" jsonschema:"WordPress plugin slug"`
}

type optionUpdateInput struct {
	Site  string `json:"site" jsonschema:"registered SSH site alias"`
	Key   string `json:"key" jsonschema:"WordPress option name"`
	Value string `json:"value" jsonschema:"new option value"`
}

type sitesOutput struct {
	Sites []Site `json:"sites"`
}
type pluginsOutput struct {
	Plugins []wpcli.Plugin `json:"plugins"`
}
type themesOutput struct {
	Themes []wpcli.Theme `json:"themes"`
}
type usersOutput struct {
	Users []wpcli.User `json:"users"`
}
type postsOutput struct {
	Posts []wpcli.Post `json:"posts"`
}
type valueOutput struct {
	Value string `json:"value"`
}
type actionOutput struct {
	Result string `json:"result"`
}

// NewServer registers a fixed catalog. All site and operation context arrives
// in each tools/call request; no conversation state is stored on the server.
func NewServer(service Service, allowWrites bool, schemaCache *mcp.SchemaCache) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "wpssh", Version: "0.1.0"}, &mcp.ServerOptions{SchemaCache: schemaCache})
	read := &mcp.ToolAnnotations{ReadOnlyHint: true}
	write := &mcp.ToolAnnotations{ReadOnlyHint: false}

	mcp.AddTool(s, &mcp.Tool{Name: "list_sites", Description: "List registered WordPress site aliases and groups", Annotations: read},
		func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, sitesOutput, error) {
			return nil, sitesOutput{Sites: service.ListSites()}, nil
		})
	mcp.AddTool(s, &mcp.Tool{Name: "list_plugins", Description: "List plugins on one WordPress site", Annotations: read},
		func(ctx context.Context, _ *mcp.CallToolRequest, in siteInput) (*mcp.CallToolResult, pluginsOutput, error) {
			out, err := run(ctx, service, in.Site, wpcli.New("plugin", "list").Format("json"), false)
			if err != nil {
				return nil, pluginsOutput{}, err
			}
			plugins, err := wpcli.ParseJSON[wpcli.Plugin](out)

			return nil, pluginsOutput{Plugins: plugins}, err
		})
	mcp.AddTool(s, &mcp.Tool{Name: "list_themes", Description: "List themes on one WordPress site", Annotations: read},
		func(ctx context.Context, _ *mcp.CallToolRequest, in siteInput) (*mcp.CallToolResult, themesOutput, error) {
			out, err := run(ctx, service, in.Site, wpcli.New("theme", "list").Format("json"), false)
			if err != nil {
				return nil, themesOutput{}, err
			}
			themes, err := wpcli.ParseJSON[wpcli.Theme](out)

			return nil, themesOutput{Themes: themes}, err
		})
	mcp.AddTool(s, &mcp.Tool{Name: "list_users", Description: "List WordPress users on one site", Annotations: read},
		func(ctx context.Context, _ *mcp.CallToolRequest, in siteInput) (*mcp.CallToolResult, usersOutput, error) {
			out, err := run(ctx, service, in.Site, wpcli.New("user", "list").Format("json"), false)
			if err != nil {
				return nil, usersOutput{}, err
			}
			users, err := wpcli.ParseJSON[wpcli.User](out)

			return nil, usersOutput{Users: users}, err
		})
	mcp.AddTool(s, &mcp.Tool{Name: "list_posts", Description: "List WordPress posts on one site", Annotations: read},
		func(ctx context.Context, _ *mcp.CallToolRequest, in siteInput) (*mcp.CallToolResult, postsOutput, error) {
			out, err := run(ctx, service, in.Site, wpcli.New("post", "list").Format("json"), false)
			if err != nil {
				return nil, postsOutput{}, err
			}
			posts, err := wpcli.ParseJSON[wpcli.Post](out)

			return nil, postsOutput{Posts: posts}, err
		})
	mcp.AddTool(s, &mcp.Tool{Name: "core_version", Description: "Get the installed WordPress core version", Annotations: read},
		func(ctx context.Context, _ *mcp.CallToolRequest, in siteInput) (*mcp.CallToolResult, valueOutput, error) {
			out, err := run(ctx, service, in.Site, wpcli.New("core", "version"), false)
			return nil, valueOutput{Value: strings.TrimSpace(out)}, err
		})
	mcp.AddTool(s, &mcp.Tool{Name: "get_option", Description: "Get one WordPress option value", Annotations: read},
		func(ctx context.Context, _ *mcp.CallToolRequest, in optionInput) (*mcp.CallToolResult, valueOutput, error) {
			if in.Key == "" {
				return nil, valueOutput{}, errKeyRequired
			}

			if err := validatePositional(in.Key); err != nil {
				return nil, valueOutput{}, err
			}
			out, err := run(ctx, service, in.Site, wpcli.New("option", "get").Arg(in.Key), false)

			return nil, valueOutput{Value: out}, err
		})

	if allowWrites {
		mcp.AddTool(s, &mcp.Tool{Name: "activate_plugin", Description: "Activate one installed WordPress plugin", Annotations: write},
			func(ctx context.Context, _ *mcp.CallToolRequest, in pluginInput) (*mcp.CallToolResult, actionOutput, error) {
				return pluginAction(ctx, service, in, "activate")
			})
		mcp.AddTool(s, &mcp.Tool{Name: "deactivate_plugin", Description: "Deactivate one WordPress plugin", Annotations: write},
			func(ctx context.Context, _ *mcp.CallToolRequest, in pluginInput) (*mcp.CallToolResult, actionOutput, error) {
				return pluginAction(ctx, service, in, "deactivate")
			})
		mcp.AddTool(s, &mcp.Tool{Name: "update_plugin", Description: "Update one WordPress plugin", Annotations: write},
			func(ctx context.Context, _ *mcp.CallToolRequest, in pluginInput) (*mcp.CallToolResult, actionOutput, error) {
				return pluginAction(ctx, service, in, "update")
			})
		mcp.AddTool(s, &mcp.Tool{Name: "update_option", Description: "Set one WordPress option value", Annotations: write},
			func(ctx context.Context, _ *mcp.CallToolRequest, in optionUpdateInput) (*mcp.CallToolResult, actionOutput, error) {
				if in.Key == "" {
					return nil, actionOutput{}, errKeyRequired
				}

				if err := validatePositional(in.Key); err != nil {
					return nil, actionOutput{}, err
				}

				if err := validatePositional(in.Value); err != nil {
					return nil, actionOutput{}, err
				}
				out, err := run(ctx, service, in.Site, wpcli.New("option", "update").Arg(in.Key).Arg(in.Value), true)

				return nil, actionOutput{Result: out}, err
			})
	}

	return s
}

func pluginAction(ctx context.Context, service Service, in pluginInput, action string) (*mcp.CallToolResult, actionOutput, error) {
	if strings.TrimSpace(in.Plugin) == "" {
		return nil, actionOutput{}, errPluginRequired
	}

	if err := validatePositional(in.Plugin); err != nil {
		return nil, actionOutput{}, err
	}
	out, err := run(ctx, service, in.Site, wpcli.New("plugin", action).Arg(in.Plugin), true)

	return nil, actionOutput{Result: out}, err
}

func validatePositional(value string) error {
	if strings.HasPrefix(strings.TrimSpace(value), "-") {
		return errUnsafeArgument
	}

	return nil
}

func run(ctx context.Context, service Service, site string, command *wpcli.Command, write bool) (string, error) {
	timeout := time.Minute
	if write {
		timeout = 10 * time.Minute
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	out, err := service.Run(ctx, site, command)
	if err != nil {
		return "", fmt.Errorf("execute WordPress command: %w", err)
	}

	return out, nil
}

// Handler serves one stateless MCP request per HTTP POST.
func Handler(service Service, allowWrites bool) http.Handler {
	schemaCache := mcp.NewSchemaCache()
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return NewServer(service, allowWrites, schemaCache)
	}, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, PropagateRequestCancellation: true})

	return http.NewCrossOriginProtection().Handler(mcpHandler)
}
