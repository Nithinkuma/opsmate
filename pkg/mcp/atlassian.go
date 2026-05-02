package mcp

import (
	"context"
	"encoding/json"
	"fmt"
)

// AtlassianClient is a typed wrapper for the Atlassian (Jira) MCP server.
type AtlassianClient struct {
	base *BaseClient
}

// NewAtlassianClient creates an Atlassian MCP client.
func NewAtlassianClient(serverURL, authToken string) (*AtlassianClient, error) {
	b, err := NewBaseClient(serverURL, authToken)
	if err != nil {
		return nil, err
	}
	return &AtlassianClient{base: b}, nil
}

// JiraTicket is a minimal representation of a Jira issue.
type JiraTicket struct {
	ID          string `json:"id"`
	Key         string `json:"key"`
	Summary     string `json:"summary"`
	Description string `json:"description"`
	Status      string `json:"status"`
	Priority    string `json:"priority"`
	Reporter    string `json:"reporter"`
	URL         string `json:"url"`
	Components  []string `json:"components,omitempty"`
	LinkedKeys  []string `json:"linked_keys,omitempty"`
}

// GetTicket fetches a Jira ticket by ID (e.g. "INFRA-1234").
func (a *AtlassianClient) GetTicket(ctx context.Context, ticketID string) (*JiraTicket, error) {
	raw, err := a.base.CallTool(ctx, "get_issue", map[string]interface{}{
		"issue_id_or_key": ticketID,
	})
	if err != nil {
		return nil, err
	}

	var ticket JiraTicket
	if err := json.Unmarshal([]byte(raw), &ticket); err != nil {
		return nil, fmt.Errorf("atlassian: parse ticket: %w", err)
	}
	return &ticket, nil
}

// AddComment posts a comment to a Jira ticket.
func (a *AtlassianClient) AddComment(ctx context.Context, ticketID, body string) error {
	_, err := a.base.CallTool(ctx, "add_comment", map[string]interface{}{
		"issue_id_or_key": ticketID,
		"body":            body,
	})
	return err
}

// TransitionTicket moves a ticket to a new status.
func (a *AtlassianClient) TransitionTicket(ctx context.Context, ticketID, transition string) error {
	_, err := a.base.CallTool(ctx, "transition_issue", map[string]interface{}{
		"issue_id_or_key": ticketID,
		"transition":      transition,
	})
	return err
}
