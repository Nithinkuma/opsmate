package tool_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nithinkuma/opsmate/pkg/tool"
	"github.com/stretchr/testify/require"
)

const validManifestYAML = `id: "test-dep-v1"
verb: "update_dependency"
schema_version: 1
repo:
  pattern: "org/test-repo"
  default_branch: "main"
runtime:
  language: "python"
  entrypoint: "script.py"
  interpreter: "python3"
preconditions: []
postconditions: []
validation:
  dry_run_command: ""
  golden_tests: ""
provenance:
  generated_by: "test"
  llm_model: "test"
hash:
  manifest: ""
  script: ""
`

const simpleScript = `import sys
print("ok")
`

func writeToolDir(t *testing.T, manifestYAML, scriptContent string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest.yaml"), []byte(manifestYAML), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "script.py"), []byte(scriptContent), 0644))
	return dir
}

func TestParseManifest_Valid(t *testing.T) {
	dir := writeToolDir(t, validManifestYAML, simpleScript)
	m, err := tool.ParseManifest(filepath.Join(dir, "manifest.yaml"))
	require.NoError(t, err)
	require.Equal(t, "test-dep-v1", m.ID)
	require.Equal(t, "update_dependency", m.Verb)
	require.Equal(t, "python", m.Runtime.Language)
	require.NotEmpty(t, m.ScriptContent)
}

func TestParseManifest_MissingFile(t *testing.T) {
	_, err := tool.ParseManifest("/nonexistent/manifest.yaml")
	require.Error(t, err)
}

func TestParseManifest_MissingID(t *testing.T) {
	bad := `verb: "update_dependency"
schema_version: 1
repo:
  pattern: "org/repo"
  default_branch: "main"
runtime:
  language: "python"
  entrypoint: "script.py"
`
	dir := writeToolDir(t, bad, simpleScript)
	_, err := tool.ParseManifest(filepath.Join(dir, "manifest.yaml"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "id is required")
}

func TestComputeAndVerifyHashes(t *testing.T) {
	dir := writeToolDir(t, validManifestYAML, simpleScript)
	m, err := tool.ParseManifest(filepath.Join(dir, "manifest.yaml"))
	require.NoError(t, err)

	// No hash stored yet — verification passes (empty hash is skipped).
	require.NoError(t, m.VerifyHashes())

	// Compute and store hash.
	m.ComputeHashes()
	require.NotEmpty(t, m.Hash.Script)

	// Now verification must pass.
	require.NoError(t, m.VerifyHashes())

	// Tamper with the hash — must fail.
	m.Hash.Script = "sha256:badhash"
	require.Error(t, m.VerifyHashes())
}

func TestLoadFromDir(t *testing.T) {
	root := t.TempDir()
	// Create two tool subdirectories.
	for _, id := range []string{"tool-a", "tool-b"} {
		sub := filepath.Join(root, id)
		require.NoError(t, os.MkdirAll(sub, 0755))
		manifest := `id: "` + id + `"
verb: "update_dependency"
schema_version: 1
repo:
  pattern: "org/repo"
  default_branch: "main"
runtime:
  language: "python"
  entrypoint: "script.py"
`
		require.NoError(t, os.WriteFile(filepath.Join(sub, "manifest.yaml"), []byte(manifest), 0644))
		require.NoError(t, os.WriteFile(filepath.Join(sub, "script.py"), []byte("print('hi')"), 0644))
	}

	src := tool.NewLocalSource(root)
	manifests, err := src.Load(nil)
	require.NoError(t, err)
	require.Len(t, manifests, 2)
}
