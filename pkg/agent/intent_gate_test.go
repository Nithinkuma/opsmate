package agent_test

import (
	"context"
	"testing"
	"time"

	"github.com/nithinkuma/opsmate/pkg/agent"
	"github.com/nithinkuma/opsmate/pkg/intent"
	"github.com/nithinkuma/opsmate/pkg/verbs"
	"github.com/stretchr/testify/require"
)

func loadVerbReg(t *testing.T) *verbs.Registry {
	t.Helper()
	reg, err := verbs.LoadSeed()
	require.NoError(t, err)
	return reg
}

func validIntent() *intent.Intent {
	return &intent.Intent{
		IntentID:      "01TEST",
		SchemaVersion: intent.SchemaVersion,
		Source: intent.Source{
			System:    "jira",
			TicketID:  "INFRA-1",
			URL:       "https://jira.example.com/browse/INFRA-1",
			Reporter:  "alice",
			FetchedAt: time.Now().UTC(),
		},
		Action: intent.Action{
			Verb:   "update_dependency",
			Target: intent.Target{Repo: "org/payments-service", Branch: "main"},
			Parameters: map[string]interface{}{
				"package":    "lodash",
				"to_version": "4.17.21",
			},
		},
		Context: intent.Context{DescriptionRaw: "bump lodash"},
		Policy:  intent.Policy{AutoMerge: false, RequireHumanApproval: true},
	}
}

func TestIntentGate_ValidIntent(t *testing.T) {
	gate := agent.NewIntentGate(loadVerbReg(t))
	require.NoError(t, gate.Check(context.Background(), validIntent()))
}

func TestIntentGate_UnknownVerb(t *testing.T) {
	gate := agent.NewIntentGate(loadVerbReg(t))
	i := validIntent()
	i.Action.Verb = "invent_quantum_computer"

	err := gate.Check(context.Background(), i)
	require.Error(t, err)

	var gateErr *agent.GateError
	require.ErrorAs(t, err, &gateErr)
	require.NotEmpty(t, gateErr.Failures)
	require.Contains(t, gateErr.Error(), "unknown verb")
}

func TestIntentGate_EmptyRepo(t *testing.T) {
	gate := agent.NewIntentGate(loadVerbReg(t))
	i := validIntent()
	i.Action.Target.Repo = ""

	err := gate.Check(context.Background(), i)
	require.Error(t, err)

	var gateErr *agent.GateError
	require.ErrorAs(t, err, &gateErr)
	// Both schema and belt-and-suspenders repo check fire.
	require.Greater(t, len(gateErr.Failures), 0)
}

func TestIntentGate_MissingRequiredParam(t *testing.T) {
	gate := agent.NewIntentGate(loadVerbReg(t))
	i := validIntent()
	// Remove a required parameter; schema should catch it.
	delete(i.Action.Parameters, "package")

	err := gate.Check(context.Background(), i)
	require.Error(t, err)

	var gateErr *agent.GateError
	require.ErrorAs(t, err, &gateErr)
	require.Contains(t, gateErr.Error(), "params:")
}

func TestIntentGate_MultipleFailures(t *testing.T) {
	gate := agent.NewIntentGate(loadVerbReg(t))
	i := &intent.Intent{
		Action: intent.Action{
			Verb:       "nonexistent_verb",
			Target:     intent.Target{Repo: "", Branch: ""},
			Parameters: map[string]interface{}{},
		},
	}

	err := gate.Check(context.Background(), i)
	require.Error(t, err)

	var gateErr *agent.GateError
	require.ErrorAs(t, err, &gateErr)
	require.Greater(t, len(gateErr.Failures), 1, "expected multiple failures")
}

func TestGateError_FormatsAllFailures(t *testing.T) {
	e := &agent.GateError{Failures: []string{"a: bad", "b: also bad"}}
	require.Contains(t, e.Error(), "a: bad")
	require.Contains(t, e.Error(), "b: also bad")
}
