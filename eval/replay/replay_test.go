//go:build !integration

package replay_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"

	"github.com/stretchr/testify/require"
)

// GoldenCase is the schema of a file in eval/golden/*.json.
type GoldenCase struct {
	Intent       json.RawMessage `json:"intent"`
	ExpectedDiff string          `json:"expected_diff"`
}

// normaliseDiff strips leading/trailing whitespace from each line and
// collapses runs of blank lines so that trivial formatting differences
// do not cause false failures.
func normaliseDiff(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		trimmed := strings.TrimRightFunc(l, unicode.IsSpace)
		out = append(out, trimmed)
	}
	// Remove trailing empty lines.
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "\n")
}

func TestGoldenCases(t *testing.T) {
	goldenDir := filepath.Join("..", "golden")
	entries, err := os.ReadDir(goldenDir)
	require.NoError(t, err, "reading golden directory")
	require.NotEmpty(t, entries, "golden directory must contain at least one case")

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}

		name := strings.TrimSuffix(e.Name(), ".json")
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(goldenDir, e.Name())
			data, err := os.ReadFile(path)
			require.NoError(t, err, "reading golden file %s", path)

			var tc GoldenCase
			require.NoError(t, json.Unmarshal(data, &tc), "parsing golden file %s", path)

			// Validate that the intent field is present and is a JSON object.
			require.NotEmpty(t, tc.Intent, "intent must not be empty in %s", path)
			var intentMap map[string]interface{}
			require.NoError(t, json.Unmarshal(tc.Intent, &intentMap), "intent must be a JSON object")

			// Validate required top-level intent fields.
			for _, field := range []string{"intent_id", "schema_version", "source", "action", "context", "policy"} {
				require.Contains(t, intentMap, field, "intent in %s must contain field %q", path, field)
			}

			// Validate the action block.
			action, ok := intentMap["action"].(map[string]interface{})
			require.True(t, ok, "action must be an object in %s", path)
			require.Contains(t, action, "verb", "action must contain verb in %s", path)
			require.Contains(t, action, "target", "action must contain target in %s", path)
			require.Contains(t, action, "parameters", "action must contain parameters in %s", path)

			// Validate expected_diff is present.
			require.NotEmpty(t, tc.ExpectedDiff, "expected_diff must not be empty in %s", path)

			// Normalise and do a basic sanity check on the diff shape.
			normalised := normaliseDiff(tc.ExpectedDiff)
			require.True(t,
				strings.HasPrefix(normalised, "---") || strings.HasPrefix(normalised, "@@"),
				"expected_diff in %s should start with a unified-diff header, got: %q",
				path, normalised[:min(len(normalised), 40)],
			)
		})
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
