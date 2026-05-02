// Package safeguards provides ReAct-loop guardrails: step budget tracking,
// duplicate-call detection, and consecutive-failure escalation.
package safeguards

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/nithinkuma/opsmate/pkg/llm"
)

// Guard enforces per-run limits on a generator loop.
type Guard struct {
	maxSteps         int
	remaining        int
	seen             map[string]bool // (name, args_hash) → already called
	consecutiveFails int
	maxConsecFails   int
}

// New creates a Guard with the given step budget.
func New(maxSteps, maxConsecFails int) *Guard {
	return &Guard{
		maxSteps:       maxSteps,
		remaining:      maxSteps,
		seen:           make(map[string]bool),
		maxConsecFails: maxConsecFails,
	}
}

// StepsLeft returns the remaining step budget.
func (g *Guard) StepsLeft() int { return g.remaining }

// Tick decrements the step counter. Returns ErrBudgetExhausted when depleted.
func (g *Guard) Tick() error {
	g.remaining--
	if g.remaining < 0 {
		return ErrBudgetExhausted
	}
	return nil
}

// CheckDuplicate returns a synthetic "already called" error message if this
// exact (name, args) was seen before, or empty string if it is novel.
func (g *Guard) CheckDuplicate(call llm.ToolCall) string {
	key := dupKey(call)
	if g.seen[key] {
		return fmt.Sprintf("ERROR: identical call (%s, %s) already executed this run — do not repeat.", call.Name, argsPreview(call.Input))
	}
	g.seen[key] = true
	return ""
}

// RecordSuccess resets the consecutive-failure counter.
func (g *Guard) RecordSuccess() { g.consecutiveFails = 0 }

// RecordFailure increments the consecutive-failure counter.
// Returns ErrTooManyConsecutiveFailures when the limit is reached.
func (g *Guard) RecordFailure() error {
	g.consecutiveFails++
	if g.consecutiveFails >= g.maxConsecFails {
		return ErrTooManyConsecutiveFailures
	}
	return nil
}

// ErrBudgetExhausted is returned when max_steps is reached.
var ErrBudgetExhausted = fmt.Errorf("safeguards: step budget exhausted")

// ErrTooManyConsecutiveFailures is returned after N back-to-back failures.
var ErrTooManyConsecutiveFailures = fmt.Errorf("safeguards: too many consecutive failures — escalating to human")

func dupKey(call llm.ToolCall) string {
	b, _ := json.Marshal(call.Input)
	h := sha256.Sum256(b)
	return fmt.Sprintf("%s:%x", call.Name, h[:8])
}

func argsPreview(input map[string]interface{}) string {
	b, _ := json.Marshal(input)
	if len(b) > 80 {
		return string(b[:80]) + "..."
	}
	return string(b)
}
