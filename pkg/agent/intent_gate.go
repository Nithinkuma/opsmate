package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/nithinkuma/opsmate/pkg/intent"
	"github.com/nithinkuma/opsmate/pkg/verbs"
)

// IntentGate validates a freshly extracted Intent for completeness and
// semantic correctness before it enters tool-resolution or the generator.
// It is the last defence before any compute-heavy stage begins.
//
// Even though IntentAgent already validates internally, this gate runs again
// so that intents constructed by any path (tests, webhooks, direct API calls)
// are always checked uniformly.
type IntentGate struct {
	verbs *verbs.Registry
}

// NewIntentGate constructs an IntentGate backed by the given verb registry.
func NewIntentGate(v *verbs.Registry) *IntentGate {
	return &IntentGate{verbs: v}
}

// GateError collects every validation failure found in a single pass so the
// caller can return all problems to the user at once.
type GateError struct {
	Failures []string
}

func (e *GateError) Error() string {
	return "intent gate: " + strings.Join(e.Failures, "; ")
}

// Check returns a *GateError when any rule is violated, nil otherwise.
// Rules (in order):
//  1. JSON schema validation — catches missing required fields and wrong types.
//  2. Verb must be registered — ensures the pipeline knows how to handle it.
//  3. Verb parameter schema — required parameters are present and well-typed.
//  4. Target repo non-empty — belt-and-suspenders; schema already enforces this.
func (g *IntentGate) Check(_ context.Context, i *intent.Intent) error {
	var failures []string

	// 1. JSON schema.
	if err := intent.Validate(i); err != nil {
		failures = append(failures, "schema: "+err.Error())
	}

	// 2. Verb must be known.
	if !g.verbs.Has(i.Action.Verb) {
		failures = append(failures, fmt.Sprintf(
			"unknown verb %q (known: %s)",
			i.Action.Verb, strings.Join(g.verbs.KnownVerbs(), ", "),
		))
	}

	// 3. Verb parameter schema (only if verb is known, to avoid double-errors).
	if g.verbs.Has(i.Action.Verb) {
		if err := g.verbs.Validate(i.Action.Verb, i.Action.Parameters); err != nil {
			failures = append(failures, "params: "+err.Error())
		}
	}

	// 4. Target repo.
	if i.Action.Target.Repo == "" {
		failures = append(failures, "action.target.repo is required")
	}

	if len(failures) > 0 {
		return &GateError{Failures: failures}
	}
	return nil
}
