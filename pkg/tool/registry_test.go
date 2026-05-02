package tool_test

import (
	"testing"

	"github.com/nithinkuma/opsmate/pkg/tool"
	"github.com/stretchr/testify/require"
)

func makeManifest(id, verb, repoPattern string) *tool.Manifest {
	m := &tool.Manifest{
		ID:            id,
		Verb:          verb,
		SchemaVersion: 1,
		Repo:          tool.RepoConfig{Pattern: repoPattern, DefaultBranch: "main"},
		Runtime:       tool.RuntimeConfig{Language: "python", Entrypoint: "script.py"},
		ScriptContent: `print("hello")`,
	}
	m.ComputeHashes()
	return m
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	reg := tool.NewRegistry()
	m := makeManifest("dep-v1", "update_dependency", "org/payments-service")
	require.NoError(t, reg.Register(m))
	require.Equal(t, 1, reg.Len())

	e, ok := reg.Get("dep-v1")
	require.True(t, ok)
	require.Equal(t, "update_dependency", e.Manifest.Verb)
}

func TestRegistry_Get_NotFound(t *testing.T) {
	reg := tool.NewRegistry()
	_, ok := reg.Get("does-not-exist")
	require.False(t, ok)
}

func TestRegistry_Resolve_Exact(t *testing.T) {
	reg := tool.NewRegistry()
	require.NoError(t, reg.Register(makeManifest("dep-v1", "update_dependency", "org/payments-service")))

	entry, status := reg.Resolve("org/payments-service", "update_dependency")
	require.Equal(t, tool.ResolveExact, status)
	require.NotNil(t, entry)
	require.Equal(t, "dep-v1", entry.Manifest.ID)
}

func TestRegistry_Resolve_GlobPattern(t *testing.T) {
	reg := tool.NewRegistry()
	// Pattern with wildcard
	require.NoError(t, reg.Register(makeManifest("dep-glob", "update_dependency", "org/*")))

	entry, status := reg.Resolve("org/any-repo", "update_dependency")
	require.Equal(t, tool.ResolvePattern, status)
	require.Equal(t, "dep-glob", entry.Manifest.ID)
}

func TestRegistry_Resolve_CrossRepoTemplate(t *testing.T) {
	reg := tool.NewRegistry()
	require.NoError(t, reg.Register(makeManifest("dep-other", "update_dependency", "other/repo")))

	// Different repo but same verb → template match
	entry, status := reg.Resolve("completely/different", "update_dependency")
	require.Equal(t, tool.ResolveTemplate, status)
	require.Equal(t, "dep-other", entry.Manifest.ID)
}

func TestRegistry_Resolve_NotFound(t *testing.T) {
	reg := tool.NewRegistry()
	require.NoError(t, reg.Register(makeManifest("dep-v1", "update_dependency", "org/repo")))

	_, status := reg.Resolve("org/repo", "rotate_secret_ref")
	require.Equal(t, tool.ResolveNotFound, status)
}

func TestRegistry_AllForVerb(t *testing.T) {
	reg := tool.NewRegistry()
	require.NoError(t, reg.Register(makeManifest("dep-a", "update_dependency", "org/a")))
	require.NoError(t, reg.Register(makeManifest("dep-b", "update_dependency", "org/b")))
	require.NoError(t, reg.Register(makeManifest("bump-a", "bump_version", "org/a")))

	entries := reg.AllForVerb("update_dependency")
	require.Len(t, entries, 2)

	entries = reg.AllForVerb("bump_version")
	require.Len(t, entries, 1)

	entries = reg.AllForVerb("nonexistent")
	require.Empty(t, entries)
}

func TestRegistry_HashMismatch(t *testing.T) {
	m := makeManifest("dep-v1", "update_dependency", "org/repo")
	m.Hash.Script = "sha256:badhash"

	reg := tool.NewRegistry()
	err := reg.Register(m)
	require.Error(t, err)
	require.Contains(t, err.Error(), "hash mismatch")
}

func TestRegistry_SetPolicyState(t *testing.T) {
	reg := tool.NewRegistry()
	require.NoError(t, reg.Register(makeManifest("dep-v1", "update_dependency", "org/repo")))

	require.NoError(t, reg.SetPolicyState("dep-v1", "auto_merge_eligible"))
	e, ok := reg.Get("dep-v1")
	require.True(t, ok)
	require.Equal(t, "auto_merge_eligible", string(e.PolicyState))
}

func TestRegistry_SetPolicyState_NotFound(t *testing.T) {
	reg := tool.NewRegistry()
	err := reg.SetPolicyState("does-not-exist", "review")
	require.Error(t, err)
}
