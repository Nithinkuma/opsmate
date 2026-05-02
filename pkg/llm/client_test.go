package llm_test

import (
	"context"
	"testing"

	"github.com/nithinkuma/opsmate/pkg/llm"
	"github.com/stretchr/testify/require"
)

func TestFakeClient_Replay(t *testing.T) {
	fake := &llm.FakeClient{
		Responses: []llm.Response{
			llm.ToolCallResponse("call_1", "emit_intent", map[string]interface{}{"verb": "update_dependency"}),
			llm.TextResponse("Done."),
		},
	}

	ctx := context.Background()

	r1, err := fake.Complete(ctx, llm.Request{Messages: []llm.Message{{Role: llm.RoleUser, Content: "hello"}}})
	require.NoError(t, err)
	require.Len(t, r1.ToolCalls, 1)
	require.Equal(t, "emit_intent", r1.ToolCalls[0].Name)

	r2, err := fake.Complete(ctx, llm.Request{Messages: []llm.Message{{Role: llm.RoleUser, Content: "done?"}}})
	require.NoError(t, err)
	require.Equal(t, "Done.", r2.Content)

	_, err = fake.Complete(ctx, llm.Request{})
	require.Error(t, err, "should error when transcript exhausted")
}

func TestFakeClient_Reset(t *testing.T) {
	fake := &llm.FakeClient{
		Responses: []llm.Response{llm.TextResponse("hi")},
	}
	ctx := context.Background()

	_, _ = fake.Complete(ctx, llm.Request{})
	fake.Reset()

	resp, err := fake.Complete(ctx, llm.Request{})
	require.NoError(t, err)
	require.Equal(t, "hi", resp.Content)
}
