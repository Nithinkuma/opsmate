package safeguards_test

import (
	"testing"

	"github.com/nithinkuma/opsmate/pkg/llm"
	"github.com/nithinkuma/opsmate/pkg/safeguards"
	"github.com/stretchr/testify/require"
)

func TestTick_Budget(t *testing.T) {
	g := safeguards.New(3, 5)
	require.NoError(t, g.Tick())
	require.NoError(t, g.Tick())
	require.NoError(t, g.Tick())
	err := g.Tick()
	require.ErrorIs(t, err, safeguards.ErrBudgetExhausted)
}

func TestCheckDuplicate(t *testing.T) {
	g := safeguards.New(10, 5)
	call := llm.ToolCall{Name: "sandbox_dry_run", Input: map[string]interface{}{"language": "python"}}

	msg := g.CheckDuplicate(call)
	require.Empty(t, msg, "first call should not be flagged")

	msg = g.CheckDuplicate(call)
	require.NotEmpty(t, msg, "second identical call should be flagged")
	require.Contains(t, msg, "already executed")
}

func TestCheckDuplicate_DifferentArgs(t *testing.T) {
	g := safeguards.New(10, 5)
	call1 := llm.ToolCall{Name: "sandbox_dry_run", Input: map[string]interface{}{"language": "python"}}
	call2 := llm.ToolCall{Name: "sandbox_dry_run", Input: map[string]interface{}{"language": "bash"}}

	require.Empty(t, g.CheckDuplicate(call1))
	require.Empty(t, g.CheckDuplicate(call2), "different args should not be flagged")
}

func TestConsecutiveFailures(t *testing.T) {
	g := safeguards.New(20, 3)
	require.NoError(t, g.RecordFailure())
	require.NoError(t, g.RecordFailure())
	err := g.RecordFailure()
	require.ErrorIs(t, err, safeguards.ErrTooManyConsecutiveFailures)
}

func TestConsecutiveFailures_ResetOnSuccess(t *testing.T) {
	g := safeguards.New(20, 3)
	require.NoError(t, g.RecordFailure())
	require.NoError(t, g.RecordFailure())
	g.RecordSuccess()
	require.NoError(t, g.RecordFailure()) // counter reset; should not error
}
