package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nithinkuma/opsmate/pkg/sandbox"
)

// GoldenCase is one (input-parameters → expected-diff) test case for a tool.
// Tools must ship at least one golden case so the health checker and the
// generator validator can verify correct behaviour before going live.
type GoldenCase struct {
	Name         string                 `json:"name"`
	Input        map[string]interface{} `json:"input"`
	ExpectedDiff string                 `json:"expected_diff"`
}

// GoldenResult holds the outcome of running one golden test case.
type GoldenResult struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Got    string `json:"got,omitempty"`
	Want   string `json:"want,omitempty"`
	Error  string `json:"error,omitempty"`
}

// LoadGoldenTests reads the golden-test file referenced by m.Validation.GoldenTests.
// Returns nil, nil when no golden test path is configured (tool has no tests yet).
func LoadGoldenTests(m *Manifest) ([]GoldenCase, error) {
	if m.Validation.GoldenTests == "" || m.Dir == "" {
		return nil, nil
	}
	path := filepath.Join(m.Dir, m.Validation.GoldenTests)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("golden: read %s: %w", path, err)
	}
	var cases []GoldenCase
	if err := json.Unmarshal(data, &cases); err != nil {
		return nil, fmt.Errorf("golden: parse %s: %w", path, err)
	}
	return cases, nil
}

// RunGoldenTests executes every case against the tool script in the sandbox.
// All cases are always run — no short-circuit on first failure.
func RunGoldenTests(ctx context.Context, runner sandbox.Runner, m *Manifest, cases []GoldenCase) []GoldenResult {
	results := make([]GoldenResult, len(cases))
	for i, c := range cases {
		inputJSON, _ := json.Marshal(c.Input)
		res, err := runner.Run(ctx, sandbox.Spec{
			Language:       sandbox.Language(m.Runtime.Language),
			ScriptContent:  m.ScriptContent,
			ParamsJSON:     string(inputJSON),
			TimeoutSeconds: 60,
		})
		r := GoldenResult{Name: c.Name, Want: c.ExpectedDiff}
		switch {
		case err != nil:
			r.Error = err.Error()
		case res.ExitCode != 0:
			r.Got = res.Stdout
			r.Error = fmt.Sprintf("exit %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr))
		default:
			r.Got = res.Stdout
			r.Passed = normGolden(r.Got) == normGolden(r.Want)
			if !r.Passed {
				r.Error = "diff mismatch"
			}
		}
		results[i] = r
	}
	return results
}

// AllPassed returns true only when every result is passing.
func AllPassed(results []GoldenResult) bool {
	for _, r := range results {
		if !r.Passed {
			return false
		}
	}
	return true
}

// SummaryLine returns a compact human-readable pass/fail count.
func SummaryLine(results []GoldenResult) string {
	pass, fail := 0, 0
	for _, r := range results {
		if r.Passed {
			pass++
		} else {
			fail++
		}
	}
	return fmt.Sprintf("%d passed, %d failed", pass, fail)
}

// FailureMessages returns one line per failed test, empty when all pass.
func FailureMessages(results []GoldenResult) []string {
	var out []string
	for _, r := range results {
		if !r.Passed {
			out = append(out, fmt.Sprintf("%s: %s", r.Name, r.Error))
		}
	}
	return out
}

func normGolden(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
