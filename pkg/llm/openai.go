package llm

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/sashabaranov/go-openai"
)

// OpenAIClient implements Client using go-openai (also works with Ollama's
// OpenAI-compatible endpoint by setting a custom BaseURL).
type OpenAIClient struct {
	client *openai.Client
	model  string
}

// NewOpenAIClient creates a client using the standard OpenAI endpoint.
func NewOpenAIClient(apiKey, model string) *OpenAIClient {
	return &OpenAIClient{client: openai.NewClient(apiKey), model: model}
}

// NewOllamaClient creates a client pointing at a local Ollama instance.
func NewOllamaClient(baseURL, model string) *OpenAIClient {
	cfg := openai.DefaultConfig("ollama")
	cfg.BaseURL = baseURL + "/v1"
	return &OpenAIClient{client: openai.NewClientWithConfig(cfg), model: model}
}

func (o *OpenAIClient) ProviderName() string { return "openai" }

func (o *OpenAIClient) Complete(ctx context.Context, req Request) (Response, error) {
	msgs := make([]openai.ChatCompletionMessage, 0, len(req.Messages))
	if req.System != "" {
		msgs = append(msgs, openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleSystem,
			Content: req.System,
		})
	}
	for _, m := range req.Messages {
		role := openai.ChatMessageRoleUser
		switch m.Role {
		case RoleAssistant:
			role = openai.ChatMessageRoleAssistant
		case RoleTool:
			role = openai.ChatMessageRoleTool
		}
		msgs = append(msgs, openai.ChatCompletionMessage{
			Role:       role,
			Content:    m.Content,
			ToolCallID: m.ToolCallID,
		})
	}

	creq := openai.ChatCompletionRequest{
		Model:       o.model,
		Messages:    msgs,
		Temperature: float32(req.Temperature),
	}
	if req.MaxTokens > 0 {
		creq.MaxTokens = req.MaxTokens
	}

	if len(req.Tools) > 0 {
		tools := make([]openai.Tool, len(req.Tools))
		for i, t := range req.Tools {
			schemaBytes, err := json.Marshal(t.InputSchema)
			if err != nil {
				return Response{}, fmt.Errorf("openai: marshal tool schema: %w", err)
			}
			tools[i] = openai.Tool{
				Type: openai.ToolTypeFunction,
				Function: &openai.FunctionDefinition{
					Name:        t.Name,
					Description: t.Description,
					Parameters:  json.RawMessage(schemaBytes),
				},
			}
		}
		creq.Tools = tools
		switch req.ToolChoice {
		case ToolChoiceRequired:
			creq.ToolChoice = "required"
		case ToolChoiceNone:
			creq.ToolChoice = "none"
		}
	}

	resp, err := o.client.CreateChatCompletion(ctx, creq)
	if err != nil {
		return Response{}, fmt.Errorf("openai: api error: %w", err)
	}

	if len(resp.Choices) == 0 {
		return Response{}, fmt.Errorf("openai: empty choices")
	}

	msg := resp.Choices[0].Message
	out := Response{
		Content: msg.Content,
		Model:   o.model,
		Usage: Usage{
			InputTokens:  resp.Usage.PromptTokens,
			OutputTokens: resp.Usage.CompletionTokens,
		},
	}
	for _, tc := range msg.ToolCalls {
		var input map[string]interface{}
		_ = json.Unmarshal([]byte(tc.Function.Arguments), &input)
		out.ToolCalls = append(out.ToolCalls, ToolCall{
			ID:    tc.ID,
			Name:  tc.Function.Name,
			Input: input,
		})
	}
	return out, nil
}
