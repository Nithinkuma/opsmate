package agent_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/nithinkuma/opsmate/pkg/agent"
	"github.com/nithinkuma/opsmate/pkg/policy"
	"github.com/nithinkuma/opsmate/pkg/tool"
	"github.com/stretchr/testify/require"
)

// writeGoldenFile writes a golden_tests.json into dir and returns the filename.
func writeGoldenFile(t *testing.T, dir string, cases []tool.GoldenCase) string {
	t.Helper()
	b, _ := json.Marshal(cases)
	path := filepath.Join(dir, "golden_tests.json")
	require.NoError(t, os.WriteFile(path, b, 0644))
	return "golden_tests.json"
}

func makeEntry(t *testing.T, script string, goldenFile string, dir string) *tool.Entry {
	t.Helper()
	m := &tool.Manifest{
		ID:            "health-test-tool",
		Verb:          "update_dependency",
		SchemaVersion: 1,
		Repo:          tool.RepoConfig{Pattern: "org/repo"},
		Runtime:       tool.RuntimeConfig{Language: "bash", Entrypoint: "script.sh"},
		ScriptContent: script,
		Dir:           dir,
	}
	if goldenFile != "" {
		m.Validation = tool.ValidationCfg{GoldenTests: goldenFile}
	}
	m.ComputeHashes()
	reg := tool.NewRegistry()
	require.NoError(t, reg.Register(m))
	entry, ok := reg.Get("health-test-tool")
	require.True(t, ok)
	entry.PolicyState = policy.StateReview
	return entry
}

func TestToolHealthChecker_NoGoldenTests(t *testing.T) {
	runner := &fakeRunner{diff: "some diff"}
	checker := agent.NewToolHealthChecker(runner)

	entry := makeEntry(t, `echo "hello"`, "", "")
	require.NoError(t, checker.Check(context.Background(), entry))
}

func TestToolHealthChecker_AllPass(t *testing.T) {
	dir := t.TempDir()
	cases := []tool.GoldenCase{
		{Name: "t1", Input: map[string]interface{}{}, ExpectedDiff: "hello"},
	}
	goldenFile := writeGoldenFile(t, dir, cases)

	// fakeRunner returns a preset diff — we need the real LocalRunner here.
	runner := realLocalRunner(t)
	checker := agent.NewToolHealthChecker(runner)
	entry := makeEntry(t, `echo "hello"`, goldenFile, dir)

	require.NoError(t, checker.Check(context.Background(), entry))
}

func TestToolHealthChecker_Failing(t *testing.T) {
	dir := t.TempDir()
	cases := []tool.GoldenCase{
		{Name: "wrong", Input: map[string]interface{}{}, ExpectedDiff: "expected output"},
	}
	goldenFile := writeGoldenFile(t, dir, cases)

	runner := realLocalRunner(t)
	checker := agent.NewToolHealthChecker(runner)
	entry := makeEntry(t, `echo "actual different output"`, goldenFile, dir)

	err := checker.Check(context.Background(), entry)
	require.Error(t, err)

	var healthErr *agent.ToolHealthError
	require.ErrorAs(t, err, &healthErr)
	require.Equal(t, "health-test-tool", healthErr.ToolID)
	require.False(t, tool.AllPassed(healthErr.Results))
}

func TestToolHealthChecker_MissingGoldenFile(t *testing.T) {
	runner := realLocalRunner(t)
	checker := agent.NewToolHealthChecker(runner)

	dir := t.TempDir()
	// Point to a file that doesn't exist.
	entry := makeEntry(t, `echo "x"`, "nonexistent.json", dir)

	err := checker.Check(context.Background(), entry)
	require.Error(t, err)
	require.Contains(t, err.Error(), "load golden tests")
}

func TestToolHealthError_Message(t *testing.T) {
	e := &agent.ToolHealthError{
		ToolID: "my-tool",
		Results: []tool.GoldenResult{
			{Name: "t1", Passed: true},
			{Name: "t2", Passed: false, Error: "diff mismatch"},
		},
	}
	msg := e.Error()
	require.Contains(t, msg, "my-tool")
	require.Contains(t, msg, "t2")
	require.Contains(t, msg, "diff mismatch")
}
