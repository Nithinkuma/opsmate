package llm

import (
	"context"
	"encoding/json"
	"fmt"
)

// FakeClient is a deterministic LLM client for tests. It replays a
// pre-recorded transcript of (request → response) pairs in order.
type FakeClient struct {
	Responses []Response
	Requests  []Request
	idx       int
}

func (f *FakeClient) ProviderName() string { return "fake" }

func (f *FakeClient) Complete(_ context.Context, req Request) (Response, error) {
	f.Requests = append(f.Requests, req)
	if f.idx >= len(f.Responses) {
		return Response{}, fmt.Errorf("fake: no response at index %d (have %d)", f.idx, len(f.Responses))
	}
	resp := f.Responses[f.idx]
	f.idx++
	return resp, nil
}

// Reset rewinds the replay pointer.
func (f *FakeClient) Reset() { f.idx = 0; f.Requests = nil }

// ToolCallResponse is a helper that builds a Response containing one tool call.
func ToolCallResponse(id, toolName string, input map[string]interface{}) Response {
	raw, _ := json.Marshal(input)
	_ = raw
	return Response{
		ToolCalls: []ToolCall{{ID: id, Name: toolName, Input: input}},
		Usage:     Usage{InputTokens: 100, OutputTokens: 50},
	}
}

// TextResponse builds a plain-text Response.
func TextResponse(content string) Response {
	return Response{
		Content: content,
		Usage:   Usage{InputTokens: 50, OutputTokens: 100},
	}
}
