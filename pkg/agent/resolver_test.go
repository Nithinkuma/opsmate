package agent_test

import (
	"testing"

	"github.com/nithinkuma/opsmate/pkg/agent"
	"github.com/nithinkuma/opsmate/pkg/intent"
	"github.com/nithinkuma/opsmate/pkg/tool"
	"github.com/stretchr/testify/require"
)

func makeTestManifest(id, verb, pattern string) *tool.Manifest {
	m := &tool.Manifest{
		ID:            id,
		Verb:          verb,
		SchemaVersion: 1,
		Repo:          tool.RepoConfig{Pattern: pattern, DefaultBranch: "main"},
		Runtime:       tool.RuntimeConfig{Language: "python", Entrypoint: "script.py"},
		ScriptContent: `print("ok")`,
	}
	m.ComputeHashes()
	return m
}

func makeTestIntent(repo, verb string) *intent.Intent {
	return &intent.Intent{
		IntentID:      "01TESTINTENTID",
		SchemaVersion: "1",
		Source:        intent.Source{System: "jira", TicketID: "TEST-1"},
		Action: intent.Action{
			Verb:       verb,
			Target:     intent.Target{Repo: repo, Branch: "main"},
			Parameters: map[string]interface{}{},
		},
	}
}

func TestResolver_ExactMatch(t *testing.T) {
	reg := tool.NewRegistry()
	require.NoError(t, reg.Register(makeTestManifest("dep-v1", "update_dependency", "org/repo")))

	r := agent.NewResolver(reg)
	result := r.Resolve(makeTestIntent("org/repo", "update_dependency"))

	require.Equal(t, tool.ResolveExact, result.Status)
	require.NotNil(t, result.Entry)
}

func TestResolver_NotFound(t *testing.T) {
	reg := tool.NewRegistry()
	r := agent.NewResolver(reg)
	result := r.Resolve(makeTestIntent("org/repo", "update_dependency"))
	require.Equal(t, tool.ResolveNotFound, result.Status)
	require.Nil(t, result.Entry)
}

func TestResolver_GlobPattern(t *testing.T) {
	reg := tool.NewRegistry()
	require.NoError(t, reg.Register(makeTestManifest("dep-glob", "update_dependency", "org/*")))

	r := agent.NewResolver(reg)
	result := r.Resolve(makeTestIntent("org/any-repo", "update_dependency"))
	require.Equal(t, tool.ResolvePattern, result.Status)
}

func TestResolver_CrossRepoTemplate(t *testing.T) {
	reg := tool.NewRegistry()
	require.NoError(t, reg.Register(makeTestManifest("dep-other", "update_dependency", "other/repo")))

	r := agent.NewResolver(reg)
	result := r.Resolve(makeTestIntent("completely/different", "update_dependency"))
	require.Equal(t, tool.ResolveTemplate, result.Status)
}
