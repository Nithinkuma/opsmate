// Package eval runs golden-test cases against the local sandbox.
// Each case is a JSON file with an Intent and an expected unified diff.
package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nithinkuma/opsmate/pkg/intent"
	"github.com/nithinkuma/opsmate/pkg/sandbox"
	"github.com/nithinkuma/opsmate/pkg/tool"
)

// Case is one eval scenario loaded from a golden JSON file.
type Case struct {
	Name         string         `json:"case_name"`
	Intent       intent.Intent  `json:"intent"`
	ExpectedDiff string         `json:"expected_diff"`
}

// Result holds the outcome of running one Case.
type Result struct {
	Name     string
	Passed   bool
	Got      string
	Error    string
	Duration time.Duration
}

// Runner executes golden test cases.
type Runner struct {
	registry *tool.Registry
	sandbox  sandbox.Runner
}

// NewRunner creates an eval Runner.
func NewRunner(registry *tool.Registry, sbx sandbox.Runner) *Runner {
	return &Runner{registry: registry, sandbox: sbx}
}

// LoadCases reads all *.json files from dir and parses them as Cases.
func LoadCases(dir string) ([]Case, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("eval: read dir %s: %w", dir, err)
	}

	var cases []Case
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("eval: read %s: %w", e.Name(), err)
		}
		var c Case
		if err := json.Unmarshal(data, &c); err != nil {
			return nil, fmt.Errorf("eval: parse %s: %w", e.Name(), err)
		}
		if c.Name == "" {
			c.Name = strings.TrimSuffix(e.Name(), ".json")
		}
		cases = append(cases, c)
	}
	return cases, nil
}

// RunAll executes every case and returns one Result per case.
func (r *Runner) RunAll(ctx context.Context, cases []Case) []Result {
	results := make([]Result, len(cases))
	for i, c := range cases {
		results[i] = r.RunOne(ctx, c)
	}
	return results
}

// RunOne executes a single golden test case.
func (r *Runner) RunOne(ctx context.Context, c Case) Result {
	start := time.Now()

	entry, status := r.registry.Resolve(c.Intent.Action.Target.Repo, c.Intent.Action.Verb)
	if status == tool.ResolveNotFound {
		return Result{
			Name:     c.Name,
			Error:    fmt.Sprintf("no tool found for (repo=%s, verb=%s)", c.Intent.Action.Target.Repo, c.Intent.Action.Verb),
			Duration: time.Since(start),
		}
	}

	paramsJSON, err := json.Marshal(c.Intent.Action.Parameters)
	if err != nil {
		return Result{Name: c.Name, Error: "marshal params: " + err.Error(), Duration: time.Since(start)}
	}

	spec := sandbox.Spec{
		ToolID:         entry.Manifest.ID,
		Language:       sandbox.Language(entry.Manifest.Runtime.Language),
		ScriptContent:  entry.Manifest.ScriptContent,
		ParamsJSON:     string(paramsJSON),
		TimeoutSeconds: 120,
	}

	res, err := r.sandbox.Run(ctx, spec)
	dur := time.Since(start)
	if err != nil {
		return Result{Name: c.Name, Got: res.Stdout, Error: err.Error(), Duration: dur}
	}
	if res.ExitCode != 0 {
		return Result{
			Name:     c.Name,
			Got:      res.Stdout,
			Error:    fmt.Sprintf("exit code %d: %s", res.ExitCode, res.Stderr),
			Duration: dur,
		}
	}

	passed := normaliseDiff(res.Stdout) == normaliseDiff(c.ExpectedDiff)
	return Result{
		Name:     c.Name,
		Passed:   passed,
		Got:      res.Stdout,
		Duration: dur,
	}
}

// Summary prints a human-readable summary to w.
func Summary(results []Result) string {
	pass, fail := 0, 0
	var lines []string
	for _, r := range results {
		if r.Error != "" {
			fail++
			lines = append(lines, fmt.Sprintf("  FAIL  %s — %s", r.Name, r.Error))
		} else if r.Passed {
			pass++
			lines = append(lines, fmt.Sprintf("  PASS  %s (%dms)", r.Name, r.Duration.Milliseconds()))
		} else {
			fail++
			lines = append(lines, fmt.Sprintf("  FAIL  %s — diff mismatch", r.Name))
		}
	}
	header := fmt.Sprintf("Results: %d/%d passed", pass, pass+fail)
	return header + "\n" + strings.Join(lines, "\n")
}

func normaliseDiff(s string) string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		t := strings.TrimRight(line, " \t\r")
		out = append(out, t)
	}
	// strip trailing blank lines
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "\n")
}
