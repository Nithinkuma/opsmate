package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/nithinkuma/opsmate/pkg/sandbox"
	"github.com/nithinkuma/opsmate/pkg/tool"
)

// ToolHealthChecker runs the golden tests bundled with a tool manifest to
// verify it still produces correct output before it executes on real data.
// A tool with no golden tests passes trivially (treated as untested, not broken).
type ToolHealthChecker struct {
	runner sandbox.Runner
}

// NewToolHealthChecker constructs a checker that executes scripts in runner.
func NewToolHealthChecker(runner sandbox.Runner) *ToolHealthChecker {
	return &ToolHealthChecker{runner: runner}
}

// ToolHealthError is returned when one or more golden tests fail.
type ToolHealthError struct {
	ToolID  string
	Results []tool.GoldenResult
}

func (e *ToolHealthError) Error() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "tool %q health check failed (%s):",
		e.ToolID, tool.SummaryLine(e.Results))
	for _, r := range e.Results {
		if !r.Passed {
			fmt.Fprintf(&sb, "\n  - %s: %s", r.Name, r.Error)
		}
	}
	return sb.String()
}

// Check loads and runs the golden tests for entry.Manifest.
//   - No golden tests configured → returns nil (pass-through; tool is untested).
//   - Any golden test fails → returns *ToolHealthError describing which failed.
func (h *ToolHealthChecker) Check(ctx context.Context, entry *tool.Entry) error {
	cases, err := tool.LoadGoldenTests(entry.Manifest)
	if err != nil {
		return fmt.Errorf("tool_health: load golden tests for %s: %w", entry.Manifest.ID, err)
	}
	if len(cases) == 0 {
		return nil
	}

	results := tool.RunGoldenTests(ctx, h.runner, entry.Manifest, cases)
	if !tool.AllPassed(results) {
		return &ToolHealthError{ToolID: entry.Manifest.ID, Results: results}
	}
	return nil
}
