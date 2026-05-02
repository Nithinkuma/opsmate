// Package llm provides a provider-agnostic interface for LLM completions.
// Internal code calls this interface; only the concrete implementations at
// the leaves know about external SDKs.
package llm

import "context"

// ToolChoice controls how the model uses tools.
const (
	ToolChoiceRequired = "required"
	ToolChoiceAuto     = "auto"
	ToolChoiceNone     = "none"
)

// Role values for Message.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// Request is a provider-agnostic completion request.
type Request struct {
	Messages    []Message `json:"messages"`
	Tools       []Tool    `json:"tools,omitempty"`
	ToolChoice  string    `json:"tool_choice,omitempty"`
	Temperature float64   `json:"temperature"`
	Model       string    `json:"model"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	System      string    `json:"system,omitempty"`
}

// Response is the normalised completion result.
type Response struct {
	Content   string     `json:"content"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	Usage     Usage      `json:"usage"`
	Model     string     `json:"model"`
}

// Message is a single turn in the conversation.
type Message struct {
	Role       string `json:"role"`
	Content    string `json:"content"`
	ToolCallID string `json:"tool_call_id,omitempty"` // set for RoleTool messages
	Name       string `json:"name,omitempty"`         // tool name for RoleTool messages
}

// Tool describes a callable function the model may invoke.
type Tool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"input_schema"` // JSON Schema object
}

// ToolCall is a model-requested function invocation.
type ToolCall struct {
	ID    string                 `json:"id"`
	Name  string                 `json:"name"`
	Input map[string]interface{} `json:"input"`
}

// Usage holds token counts and estimated cost for a single call.
type Usage struct {
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	CostUSD      float64 `json:"cost_usd"`
}

// Client is the single interface every LLM provider must satisfy.
type Client interface {
	Complete(ctx context.Context, req Request) (Response, error)
	// ProviderName is used for observability labelling.
	ProviderName() string
}
