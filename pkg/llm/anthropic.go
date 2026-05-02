package llm

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// costPer1MTokens maps model prefix → [input, output] cost in USD.
var costPer1MTokens = map[string][2]float64{
	"claude-opus-4":   {15.0, 75.0},
	"claude-sonnet-4": {3.0, 15.0},
	"claude-haiku-4":  {0.25, 1.25},
}

// AnthropicClient implements Client using the official Anthropic Go SDK.
type AnthropicClient struct {
	client anthropic.Client
	model  string
}

// NewAnthropicClient creates a ready-to-use Anthropic LLM client.
func NewAnthropicClient(apiKey, model string) *AnthropicClient {
	c := anthropic.NewClient(option.WithAPIKey(apiKey))
	return &AnthropicClient{client: c, model: model}
}

func (a *AnthropicClient) ProviderName() string { return "anthropic" }

func (a *AnthropicClient) Complete(ctx context.Context, req Request) (Response, error) {
	maxTok := req.MaxTokens
	if maxTok == 0 {
		maxTok = 8096
	}

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(req.Model),
		MaxTokens: int64(maxTok),
	}

	if req.System != "" {
		params.System = []anthropic.TextBlockParam{
			{Text: req.System},
		}
	}

	// Build messages.
	for _, m := range req.Messages {
		switch m.Role {
		case RoleUser:
			params.Messages = append(params.Messages, anthropic.NewUserMessage(
				anthropic.NewTextBlock(m.Content),
			))
		case RoleAssistant:
			params.Messages = append(params.Messages, anthropic.NewAssistantMessage(
				anthropic.NewTextBlock(m.Content),
			))
		case RoleTool:
			params.Messages = append(params.Messages, anthropic.NewUserMessage(
				anthropic.NewToolResultBlock(m.ToolCallID, m.Content, false),
			))
		}
	}

	// Build tools.
	if len(req.Tools) > 0 {
		tools := make([]anthropic.ToolUnionParam, len(req.Tools))
		for i, t := range req.Tools {
			schemaBytes, err := json.Marshal(t.InputSchema)
			if err != nil {
				return Response{}, fmt.Errorf("anthropic: marshal schema for %s: %w", t.Name, err)
			}
			tools[i] = anthropic.ToolUnionParam{
				OfTool: &anthropic.ToolParam{
					Name:        t.Name,
					Description: anthropic.String(t.Description),
					InputSchema: anthropic.ToolInputSchemaParam{
						Properties: json.RawMessage(schemaBytes),
					},
				},
			}
		}
		params.Tools = tools

		switch req.ToolChoice {
		case ToolChoiceRequired:
			params.ToolChoice = anthropic.ToolChoiceUnionParam{
				OfAny: &anthropic.ToolChoiceAnyParam{Type: "any"},
			}
		case ToolChoiceNone:
			params.ToolChoice = anthropic.ToolChoiceUnionParam{
				OfNone: &anthropic.ToolChoiceNoneParam{Type: "none"},
			}
		}
	}

	msg, err := a.client.Messages.New(ctx, params)
	if err != nil {
		return Response{}, fmt.Errorf("anthropic: api error: %w", err)
	}

	resp := Response{Model: a.model}
	for _, block := range msg.Content {
		switch b := block.AsAny().(type) {
		case anthropic.TextBlock:
			resp.Content += b.Text
		case anthropic.ToolUseBlock:
			var input map[string]interface{}
			_ = json.Unmarshal([]byte(b.Input), &input)
			resp.ToolCalls = append(resp.ToolCalls, ToolCall{
				ID:    b.ID,
				Name:  b.Name,
				Input: input,
			})
		}
	}

	resp.Usage = Usage{
		InputTokens:  int(msg.Usage.InputTokens),
		OutputTokens: int(msg.Usage.OutputTokens),
		CostUSD:      estimateCost(a.model, int(msg.Usage.InputTokens), int(msg.Usage.OutputTokens)),
	}
	return resp, nil
}

func estimateCost(model string, in, out int) float64 {
	for prefix, costs := range costPer1MTokens {
		if len(model) >= len(prefix) && model[:len(prefix)] == prefix {
			return float64(in)/1_000_000*costs[0] + float64(out)/1_000_000*costs[1]
		}
	}
	return 0
}
