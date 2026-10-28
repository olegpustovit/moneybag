package mcpserver

import (
	"context"
	"fmt"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type tokenTransport struct {
	file string
	next http.RoundTripper
}

func (t tokenTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	f, e := os.Open(t.file)
	if e != nil {
		return nil, fmt.Errorf("open MCP token file")
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("MCP token file must be a private regular file (0600)")
	}
	raw, e := io.ReadAll(io.LimitReader(f, 8193))
	token := strings.TrimSpace(string(raw))
	if e != nil || len(raw) > 8192 || token == "" || strings.ContainsAny(token, "\r\n") {
		return nil, fmt.Errorf("invalid MCP token file")
	}
	clone := r.Clone(r.Context())
	clone.Header.Set("Authorization", "Bearer "+token)
	return t.next.RoundTrip(clone)
}

// Proxy keeps all writes in the API process; stdio never connects to MongoDB.
func Proxy(ctx context.Context, endpoint, tokenFile string, log *slog.Logger) (*mcp.Server, *mcp.ClientSession, error) {
	u, e := url.Parse(endpoint)
	if e != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "/mcp" && u.Path != "/mcp/readonly") || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1"))) {
		return nil, nil, fmt.Errorf("MONEYBAG_MCP_URL must be an HTTPS MCP endpoint or HTTP loopback endpoint")
	}
	if tokenFile == "" {
		return nil, nil, fmt.Errorf("MONEYBAG_MCP_TOKEN_FILE is required")
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "moneybag-stdio", Version: "1"}, &mcp.ClientOptions{Logger: log})
	remote, e := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint, HTTPClient: &http.Client{Timeout: 45 * time.Second, Transport: tokenTransport{tokenFile, http.DefaultTransport}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, DisableStandaloneSSE: true}, nil)
	if e != nil {
		return nil, nil, fmt.Errorf("connect MCP endpoint: %w", e)
	}
	success := false
	defer func() {
		if !success {
			remote.Close()
		}
	}()
	server := mcp.NewServer(&mcp.Implementation{Name: "moneybag-stdio", Version: "1"}, &mcp.ServerOptions{Logger: log, Instructions: "Moneybag stdio forwards to your authenticated API. All amounts are decimal strings. Writes create drafts unless explicitly committed; use immutable reversals to correct posted entries."})
	for tool, e := range remote.Tools(ctx, nil) {
		if e != nil {
			return nil, nil, e
		}
		server.AddTool(tool, func(ctx context.Context, r *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return remote.CallTool(ctx, &mcp.CallToolParams{Name: r.Params.Name, Arguments: r.Params.Arguments})
		})
	}
	read := func(ctx context.Context, r *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return remote.ReadResource(ctx, r.Params)
	}
	for resource, e := range remote.Resources(ctx, nil) {
		if e != nil {
			return nil, nil, e
		}
		server.AddResource(resource, read)
	}
	for resource, e := range remote.ResourceTemplates(ctx, nil) {
		if e != nil {
			return nil, nil, e
		}
		server.AddResourceTemplate(resource, read)
	}
	for prompt, e := range remote.Prompts(ctx, nil) {
		if e != nil {
			return nil, nil, e
		}
		server.AddPrompt(prompt, func(ctx context.Context, r *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			return remote.GetPrompt(ctx, r.Params)
		})
	}
	success = true
	return server, remote, nil
}
