package agent_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/nithinkuma/opsmate/pkg/agent"
	"github.com/nithinkuma/opsmate/pkg/intent"
	"github.com/nithinkuma/opsmate/pkg/llm"
	"github.com/nithinkuma/opsmate/pkg/policy"
	"github.com/nithinkuma/opsmate/pkg/sandbox"
	"github.com/nithinkuma/opsmate/pkg/tool"
	"github.com/nithinkuma/opsmate/pkg/verbs"
	"github.com/stretchr/testify/require"
)

// fakeRunner is a sandbox.Runner that records specs and returns a preset diff.
type fakeRunner struct {
	spec sandbox.Spec
	diff string
	fail bool
}

func (f *fakeRunner) Run(_ context.Context, s sandbox.Spec) (sandbox.Result, error) {
	f.spec = s
	now := time.Now()
	if f.fail {
		return sandbox.Result{ExitCode: 1, Stderr: "injected failure", FinishedAt: now}, nil
	}
	return sandbox.Result{ExitCode: 0, Stdout: f.diff, FinishedAt: now}, nil
}

// fakeAtlassian satisfies intent_agent's atlassian calls without MCP.
// We exercise the Resolver and Executor directly in this test.

func makeRegistryWithTool(t *testing.T) (*tool.Registry, *tool.Manifest) {
	t.Helper()
	m := &tool.Manifest{
		ID:            "dep-v1",
		Verb:          "update_dependency",
		SchemaVersion: 1,
		Repo:          tool.RepoConfig{Pattern: "org/payments-service", DefaultBranch: "main"},
		Runtime:       tool.RuntimeConfig{Language: "bash", Entrypoint: "script.sh"},
		ScriptContent: `echo "--- a/package.json\n+++ b/package.json"`,
	}
	m.ComputeHashes()
	reg := tool.NewRegistry()
	require.NoError(t, reg.Register(m))
	return reg, m
}

// TestResolver_FullChain exercises Resolver → Entry lookup end-to-end.
func TestResolver_FullChain(t *testing.T) {
	reg, _ := makeRegistryWithTool(t)
	resolver := agent.NewResolver(reg)

	i := &intent.Intent{
		Action: intent.Action{
			Verb:   "update_dependency",
			Target: intent.Target{Repo: "org/payments-service", Branch: "main"},
		},
	}

	result := resolver.Resolve(i)
	require.Equal(t, tool.ResolveExact, result.Status)
	require.Equal(t, "dep-v1", result.Entry.Manifest.ID)
}

// TestExecutor_Success exercises Execute with a fakeRunner.
func TestExecutor_Success(t *testing.T) {
	reg, _ := makeRegistryWithTool(t)
	runner := &fakeRunner{diff: "--- a/package.json\n+++ b/package.json\n@@ -1 +1 @@\n-lodash@4.17.20\n+lodash@4.17.21"}
	log := slog.Default()

	executor := agent.NewExecutor(runner, nil, reg, "local", log)

	i := &intent.Intent{
		IntentID: "01TEST",
		Action: intent.Action{
			Verb:       "update_dependency",
			Target:     intent.Target{Repo: "org/payments-service", Branch: "main"},
			Parameters: map[string]interface{}{"package": "lodash", "to_version": "4.17.21"},
		},
	}

	entry, _ := reg.Get("dep-v1")
	result, err := executor.Execute(context.Background(), i, entry)
	require.NoError(t, err)
	require.NotEmpty(t, result.ExecutionID)
	require.Contains(t, result.Diff, "lodash@4.17.21")
}

// TestExecutor_DemotesOnFailure verifies trust-ladder demotion.
func TestExecutor_DemotesOnFailure(t *testing.T) {
	reg, _ := makeRegistryWithTool(t)

	// Promote to auto_merge_active first.
	require.NoError(t, reg.SetPolicyState("dep-v1", policy.StateAutoMergeActive))

	runner := &fakeRunner{fail: true}
	executor := agent.NewExecutor(runner, nil, reg, "local", slog.Default())

	i := &intent.Intent{
		IntentID: "01TEST",
		Action: intent.Action{
			Verb:       "update_dependency",
			Target:     intent.Target{Repo: "org/payments-service", Branch: "main"},
			Parameters: map[string]interface{}{},
		},
	}

	entry, _ := reg.Get("dep-v1")
	_, err := executor.Execute(context.Background(), i, entry)
	require.Error(t, err) // execution failed

	// In-memory state must have been demoted one level.
	e, _ := reg.Get("dep-v1")
	require.Equal(t, policy.StateAutoMergeEligible, e.PolicyState)
}

// TestVerbRegistry_SeedContains15Verbs verifies the embedded verb schemas are
// all loadable.
func TestVerbRegistry_SeedContains15Verbs(t *testing.T) {
	reg, err := verbs.LoadSeed()
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(reg.KnownVerbs()), 15)
}

// TestGenerator_EscalatesWhenNoResponses verifies the generator escalates
// cleanly when the fake LLM runs out of responses.
func TestGenerator_EscalatesWhenNoResponses(t *testing.T) {
	fake := &llm.FakeClient{
		Responses: []llm.Response{
			// Model produces no tool call — causes immediate escalation.
			llm.TextResponse("I give up"),
		},
	}
	reg := tool.NewRegistry()
	runner := &fakeRunner{}
	gen := agent.NewGenerator(fake, fake, nil, reg, runner, agent.GeneratorConfig{
		MaxSteps:         5,
		MaxParallelTools: 2,
		Model:            "test",
	}, slog.Default())

	i := &intent.Intent{
		Source: intent.Source{TicketID: "T-1"},
		Action: intent.Action{
			Verb:   "update_dependency",
			Target: intent.Target{Repo: "org/repo", Branch: "main"},
		},
	}

	_, err := gen.Generate(context.Background(), i)
	require.Error(t, err)
	var escErr *agent.EscalationError
	require.ErrorAs(t, err, &escErr)
}
