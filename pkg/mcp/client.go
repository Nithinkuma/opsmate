// Package mcp provides thin typed wrappers over external MCP servers.
// Only trust-boundary crossings (Bitbucket, Atlassian) use MCP.
// Internal agent tools use native Go interfaces.
package mcp

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

// BaseClient wraps mark3labs/mcp-go with convenience helpers.
type BaseClient struct {
	c      *client.Client
	server string
}

// NewBaseClient connects to an MCP server at the given URL.
func NewBaseClient(serverURL, authToken string) (*BaseClient, error) {
	c, err := client.NewSSEMCPClient(serverURL + "/sse")
	if err != nil {
		return nil, fmt.Errorf("mcp: connect to %s: %w", serverURL, err)
	}
	return &BaseClient{c: c, server: serverURL}, nil
}

// CallTool invokes a named tool on the MCP server and returns the result text.
func (b *BaseClient) CallTool(ctx context.Context, toolName string, args map[string]interface{}) (string, error) {
	req := mcp.CallToolRequest{}
	req.Params.Name = toolName
	req.Params.Arguments = args

	res, err := b.c.CallTool(ctx, req)
	if err != nil {
		return "", fmt.Errorf("mcp: %s.%s: %w", b.server, toolName, err)
	}
	if res.IsError {
		return "", fmt.Errorf("mcp: %s.%s returned error", b.server, toolName)
	}

	// Collect text from all content blocks
	var out string
	for _, block := range res.Content {
		if tc, ok := block.(mcp.TextContent); ok {
			out += tc.Text
		}
	}
	return out, nil
}
