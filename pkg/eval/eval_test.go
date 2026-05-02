package eval_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nithinkuma/opsmate/pkg/eval"
	"github.com/nithinkuma/opsmate/pkg/intent"
	"github.com/nithinkuma/opsmate/pkg/sandbox"
	"github.com/nithinkuma/opsmate/pkg/tool"
	"github.com/stretchr/testify/require"
)

// fixedRunner returns a preset diff regardless of spec.
type fixedRunner struct{ diff string }

func (f *fixedRunner) Run(_ context.Context, _ sandbox.Spec) (sandbox.Result, error) {
	return sandbox.Result{ExitCode: 0, Stdout: f.diff, FinishedAt: time.Now()}, nil
}

func makeRegistry(t *testing.T, diff string) *tool.Registry {
	t.Helper()
	m := &tool.Manifest{
		ID:            "dep-test",
		Verb:          "update_dependency",
		SchemaVersion: 1,
		Repo:          tool.RepoConfig{Pattern: "org/payments-service", DefaultBranch: "main"},
		Runtime:       tool.RuntimeConfig{Language: "bash", Entrypoint: "script.sh"},
		ScriptContent: `echo "` + diff + `"`,
	}
	m.ComputeHashes()
	reg := tool.NewRegistry()
	require.NoError(t, reg.Register(m))
	return reg
}

func TestLoadCases_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	cases, err := eval.LoadCases(dir)
	require.NoError(t, err)
	require.Empty(t, cases)
}

func TestLoadCases_ValidJSON(t *testing.T) {
	dir := t.TempDir()
	content := `{
		"case_name": "bump-lodash",
		"intent": {
			"intent_id": "01TEST",
			"schema_version": "1",
			"source": {"system":"jira","ticket_id":"T-1","url":"http://x","reporter":"r","fetched_at":"2024-01-01T00:00:00Z"},
			"action": {"verb":"update_dependency","target":{"repo":"org/payments-service","branch":"main"},"parameters":{"package":"lodash","to_version":"4.17.21"}},
			"context": {"description_raw":"bump lodash"},
			"policy": {"auto_merge":false,"require_human_approval":true}
		},
		"expected_diff": "--- a/package.json\n+++ b/package.json\n"
	}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "case1.json"), []byte(content), 0644))

	cases, err := eval.LoadCases(dir)
	require.NoError(t, err)
	require.Len(t, cases, 1)
	require.Equal(t, "bump-lodash", cases[0].Name)
}

func TestRunOne_Pass(t *testing.T) {
	expectedDiff := "--- a/package.json\n+++ b/package.json\n"
	reg := makeRegistry(t, expectedDiff)
	runner := &fixedRunner{diff: expectedDiff}
	r := eval.NewRunner(reg, runner)

	c := eval.Case{
		Name: "test",
		Intent: intent.Intent{
			Action: intent.Action{
				Verb:       "update_dependency",
				Target:     intent.Target{Repo: "org/payments-service", Branch: "main"},
				Parameters: map[string]interface{}{"package": "lodash"},
			},
		},
		ExpectedDiff: expectedDiff,
	}

	result := r.RunOne(context.Background(), c)
	require.Empty(t, result.Error)
	require.True(t, result.Passed)
}

func TestRunOne_Fail_DiffMismatch(t *testing.T) {
	reg := makeRegistry(t, "actual diff")
	runner := &fixedRunner{diff: "actual diff"}
	r := eval.NewRunner(reg, runner)

	c := eval.Case{
		Name: "mismatch",
		Intent: intent.Intent{
			Action: intent.Action{
				Verb:   "update_dependency",
				Target: intent.Target{Repo: "org/payments-service", Branch: "main"},
			},
		},
		ExpectedDiff: "expected diff",
	}

	result := r.RunOne(context.Background(), c)
	require.False(t, result.Passed)
}

func TestRunOne_NoTool(t *testing.T) {
	reg := tool.NewRegistry() // empty registry
	runner := &fixedRunner{}
	r := eval.NewRunner(reg, runner)

	c := eval.Case{
		Name: "no-tool",
		Intent: intent.Intent{
			Action: intent.Action{
				Verb:   "update_dependency",
				Target: intent.Target{Repo: "org/unknown", Branch: "main"},
			},
		},
	}

	result := r.RunOne(context.Background(), c)
	require.False(t, result.Passed)
	require.NotEmpty(t, result.Error)
}
