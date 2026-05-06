package tool_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/nithinkuma/opsmate/pkg/sandbox"
	"github.com/nithinkuma/opsmate/pkg/tool"
	"github.com/stretchr/testify/require"
)

func TestLoadGoldenTests_NoValidationField(t *testing.T) {
	m := &tool.Manifest{}
	cases, err := tool.LoadGoldenTests(m)
	require.NoError(t, err)
	require.Nil(t, cases)
}

func TestLoadGoldenTests_EmptyGoldenTestsPath(t *testing.T) {
	m := &tool.Manifest{Dir: t.TempDir(), Validation: tool.ValidationCfg{GoldenTests: ""}}
	cases, err := tool.LoadGoldenTests(m)
	require.NoError(t, err)
	require.Nil(t, cases)
}

func TestLoadGoldenTests_ValidFile(t *testing.T) {
	dir := t.TempDir()
	content := `[
		{"name":"bump lodash","input":{"package":"lodash","to_version":"4.17.21"},"expected_diff":"--- a/package.json\n+++ b/package.json\n"}
	]`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tests.json"), []byte(content), 0644))

	m := &tool.Manifest{Dir: dir, Validation: tool.ValidationCfg{GoldenTests: "tests.json"}}
	cases, err := tool.LoadGoldenTests(m)
	require.NoError(t, err)
	require.Len(t, cases, 1)
	require.Equal(t, "bump lodash", cases[0].Name)
	require.Equal(t, "lodash", cases[0].Input["package"])
}

func TestLoadGoldenTests_MissingFile(t *testing.T) {
	m := &tool.Manifest{Dir: t.TempDir(), Validation: tool.ValidationCfg{GoldenTests: "nonexistent.json"}}
	_, err := tool.LoadGoldenTests(m)
	require.Error(t, err)
}

func TestLoadGoldenTests_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bad.json"), []byte(`not json`), 0644))
	m := &tool.Manifest{Dir: dir, Validation: tool.ValidationCfg{GoldenTests: "bad.json"}}
	_, err := tool.LoadGoldenTests(m)
	require.Error(t, err)
}

func TestRunGoldenTests_AllPass(t *testing.T) {
	m := &tool.Manifest{
		Runtime:       tool.RuntimeConfig{Language: "bash"},
		ScriptContent: `echo "diff output"`,
	}
	cases := []tool.GoldenCase{
		{Name: "t1", Input: map[string]interface{}{}, ExpectedDiff: "diff output"},
	}
	runner := sandbox.NewLocalRunner(slog.Default())
	results := tool.RunGoldenTests(context.Background(), runner, m, cases)
	require.Len(t, results, 1)
	require.True(t, results[0].Passed, "expected pass, got error: %s", results[0].Error)
	require.True(t, tool.AllPassed(results))
}

func TestRunGoldenTests_DiffMismatch(t *testing.T) {
	m := &tool.Manifest{
		Runtime:       tool.RuntimeConfig{Language: "bash"},
		ScriptContent: `echo "wrong output"`,
	}
	cases := []tool.GoldenCase{
		{Name: "mismatch", Input: map[string]interface{}{}, ExpectedDiff: "expected output"},
	}
	runner := sandbox.NewLocalRunner(slog.Default())
	results := tool.RunGoldenTests(context.Background(), runner, m, cases)
	require.False(t, results[0].Passed)
	require.Equal(t, "diff mismatch", results[0].Error)
	require.False(t, tool.AllPassed(results))
}

func TestRunGoldenTests_ScriptExitsNonZero(t *testing.T) {
	m := &tool.Manifest{
		Runtime:       tool.RuntimeConfig{Language: "bash"},
		ScriptContent: `echo "err" >&2; exit 1`,
	}
	cases := []tool.GoldenCase{
		{Name: "failing", Input: map[string]interface{}{}, ExpectedDiff: ""},
	}
	runner := sandbox.NewLocalRunner(slog.Default())
	results := tool.RunGoldenTests(context.Background(), runner, m, cases)
	require.False(t, results[0].Passed)
	require.Contains(t, results[0].Error, "exit 1")
}

func TestRunGoldenTests_ReadsParamsPath(t *testing.T) {
	m := &tool.Manifest{
		Runtime:       tool.RuntimeConfig{Language: "bash"},
		ScriptContent: `jq -r .package "$PARAMS_PATH"`,
	}
	cases := []tool.GoldenCase{
		{Name: "params", Input: map[string]interface{}{"package": "lodash"}, ExpectedDiff: "lodash"},
	}
	runner := sandbox.NewLocalRunner(slog.Default())
	results := tool.RunGoldenTests(context.Background(), runner, m, cases)
	require.True(t, results[0].Passed, "error: %s got=%q", results[0].Error, results[0].Got)
}

func TestRunGoldenTests_MultiplePartialFail(t *testing.T) {
	m := &tool.Manifest{
		Runtime:       tool.RuntimeConfig{Language: "bash"},
		ScriptContent: `echo "hello"`,
	}
	cases := []tool.GoldenCase{
		{Name: "pass", Input: map[string]interface{}{}, ExpectedDiff: "hello"},
		{Name: "fail", Input: map[string]interface{}{}, ExpectedDiff: "world"},
	}
	runner := sandbox.NewLocalRunner(slog.Default())
	results := tool.RunGoldenTests(context.Background(), runner, m, cases)
	require.True(t, results[0].Passed)
	require.False(t, results[1].Passed)
	require.False(t, tool.AllPassed(results))
	require.Equal(t, "2 passed, 0 failed", tool.SummaryLine([]tool.GoldenResult{{Passed: true}, {Passed: true}}))
	require.Equal(t, "1 passed, 1 failed", tool.SummaryLine(results))
}

func TestAllPassed_NilSlice(t *testing.T) {
	require.True(t, tool.AllPassed(nil))
}

func TestAllPassed_Empty(t *testing.T) {
	require.True(t, tool.AllPassed([]tool.GoldenResult{}))
}

func TestParseManifestFromString_Valid(t *testing.T) {
	yaml := `
id: my-tool
verb: update_dependency
schema_version: 1
repo:
  pattern: "org/repo"
runtime:
  language: bash
  entrypoint: script.sh
`
	m, err := tool.ParseManifestFromString(yaml, `echo "hello"`, "bash")
	require.NoError(t, err)
	require.Equal(t, "my-tool", m.ID)
	require.Equal(t, "bash", m.Runtime.Language)
}

func TestParseManifestFromString_MissingID(t *testing.T) {
	yaml := `
verb: update_dependency
repo:
  pattern: "org/repo"
runtime:
  language: bash
`
	_, err := tool.ParseManifestFromString(yaml, `echo "x"`, "bash")
	require.Error(t, err)
	require.Contains(t, err.Error(), "id is required")
}

func TestParseManifestFromString_InvalidYAML(t *testing.T) {
	_, err := tool.ParseManifestFromString("not: valid: yaml: [", `echo "x"`, "bash")
	require.Error(t, err)
}

func TestParseManifestFromString_LanguageOverride(t *testing.T) {
	yaml := `
id: tool-x
verb: do_thing
repo:
  pattern: "org/x"
runtime:
  language: python
  entrypoint: script.py
`
	// language param overrides whatever is in the YAML
	m, err := tool.ParseManifestFromString(yaml, `print("hi")`, "python")
	require.NoError(t, err)
	require.Equal(t, "python", m.Runtime.Language)
}
