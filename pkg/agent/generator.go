package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	_ "embed"

	"github.com/nithinkuma/opsmate/pkg/intent"
	"github.com/nithinkuma/opsmate/pkg/llm"
	"github.com/nithinkuma/opsmate/pkg/mcp"
	"github.com/nithinkuma/opsmate/pkg/observability"
	"github.com/nithinkuma/opsmate/pkg/safeguards"
	"github.com/nithinkuma/opsmate/pkg/sandbox"
	"github.com/nithinkuma/opsmate/pkg/tool"
	"golang.org/x/sync/errgroup"
)

//go:embed prompts/generator.system.md
var generatorSystemPrompt string

// minGoldenTests is the minimum number of golden test cases the LLM must
// provide before emit_tool is accepted. Enforced server-side so the model
// cannot skip testing even if it tries.
const minGoldenTests = 1

// GeneratorConfig holds tuning parameters for the ReAct loop.
type GeneratorConfig struct {
	MaxSteps             int
	MaxParallelTools     int
	Temperature          float64
	Model                string
	ToolsRegistryRepo    string // Bitbucket "workspace/repo-slug" for the tools-registry
	ToolsRegistryBranch  string // default branch of tools-registry (usually "main")
}

// ToolPR is the output of a successful generator run.
type ToolPR struct {
	ManifestYAML string
	ScriptB64    string
	GoldenTests  []GoldenTest
	// PRUrl is set after the tools-registry PR is opened.
	PRUrl string
}

// GoldenTest is one (input, expected_diff) pair.
type GoldenTest struct {
	Name         string `json:"name"`
	InputJSON    string `json:"input_json"`
	ExpectedDiff string `json:"expected_diff"`
}

// EscalationError is returned when the generator cannot produce a valid tool.
type EscalationError struct {
	Reason string
	Steps  int
}

func (e *EscalationError) Error() string {
	return fmt.Sprintf("generator escalated to human after %d steps: %s", e.Steps, e.Reason)
}

// Generator runs the bounded ReAct loop that produces new tools.
type Generator struct {
	primary     llm.Client
	fast        llm.Client
	bitbucket   *mcp.BitbucketClient
	toolReg     *tool.Registry
	sandboxRunner sandbox.Runner
	cfg         GeneratorConfig
	log         *slog.Logger
}

// NewGenerator constructs the generator with all dependencies.
func NewGenerator(
	primary, fast llm.Client,
	bitbucket *mcp.BitbucketClient,
	toolReg *tool.Registry,
	sandboxRunner sandbox.Runner,
	cfg GeneratorConfig,
	log *slog.Logger,
) *Generator {
	return &Generator{
		primary:     primary,
		fast:        fast,
		bitbucket:   bitbucket,
		toolReg:     toolReg,
		sandboxRunner: sandboxRunner,
		cfg:         cfg,
		log:         log,
	}
}

// Generate runs the ReAct loop for a cache-miss intent and returns the
// generated tool, or an EscalationError if it cannot succeed.
func (g *Generator) Generate(ctx context.Context, i *intent.Intent) (*ToolPR, error) {
	ctx, span := observability.Start(ctx, "generator.generate")
	defer span.End()

	guard := safeguards.New(g.cfg.MaxSteps, 3)
	tools := g.buildTools()

	messages := []llm.Message{
		{
			Role: llm.RoleUser,
			Content: fmt.Sprintf(
				"Generate a tool for this intent:\n\n```json\n%s\n```",
				mustJSON(i),
			),
		},
	}

	var emittedTool *ToolPR

	for {
		if err := guard.Tick(); err != nil {
			return nil, &EscalationError{
				Reason: "step budget exhausted",
				Steps:  g.cfg.MaxSteps - guard.StepsLeft(),
			}
		}

		req := llm.Request{
			Model:       g.cfg.Model,
			Temperature: g.cfg.Temperature,
			Tools:       tools,
			Messages:    messages,
			System:      generatorSystemPrompt,
		}

		resp, err := g.primary.Complete(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("generator: llm error: %w", err)
		}

		// No tool calls → model gave up.
		if len(resp.ToolCalls) == 0 {
			return nil, &EscalationError{
				Reason: "model produced no tool call: " + resp.Content,
				Steps:  g.cfg.MaxSteps - guard.StepsLeft(),
			}
		}

		// Append assistant turn.
		messages = append(messages, llm.Message{
			Role:    llm.RoleAssistant,
			Content: resp.Content,
		})

		// ── Process tool calls ────────────────────────────────────────────────
		// Check for emit_tool first; execute the rest in parallel.
		var regularCalls []llm.ToolCall
		for _, call := range resp.ToolCalls {
			if call.Name == "emit_tool" {
				result, done, err := g.handleEmitTool(ctx, call, guard, i)
				messages = append(messages, llm.Message{
					Role:       llm.RoleTool,
					ToolCallID: call.ID,
					Name:       call.Name,
					Content:    result,
				})
				if done && err == nil {
					emittedTool = &ToolPR{
						ManifestYAML: strInput(call.Input, "manifest_yaml"),
						ScriptB64:    strInput(call.Input, "script_b64"),
					}
					if gt, _ := call.Input["golden_tests"].([]interface{}); len(gt) > 0 {
						b, _ := json.Marshal(gt)
						_ = json.Unmarshal(b, &emittedTool.GoldenTests)
					}
					return emittedTool, nil
				}
				if err != nil {
					if err == safeguards.ErrTooManyConsecutiveFailures {
						return nil, &EscalationError{
							Reason: "3 consecutive emit_tool failures",
							Steps:  g.cfg.MaxSteps - guard.StepsLeft(),
						}
					}
				}
			} else {
				regularCalls = append(regularCalls, call)
			}
		}

		// Dedup + execute regular calls in parallel.
		results := g.executeParallel(ctx, regularCalls, guard)
		for callID, result := range results {
			// Find the call name.
			name := ""
			for _, c := range regularCalls {
				if c.ID == callID {
					name = c.Name
					break
				}
			}
			messages = append(messages, llm.Message{
				Role:       llm.RoleTool,
				ToolCallID: callID,
				Name:       name,
				Content:    result,
			})
		}
	}
}

// handleEmitTool validates the emit_tool call end-to-end before accepting it:
//  1. Presence check — manifest_yaml and script_b64 must be non-empty.
//  2. Base64 decode — script must be valid base64.
//  3. Manifest parse — YAML must parse and pass field validation.
//  4. Golden tests required — at least minGoldenTests cases must be provided.
//  5. Golden tests pass — every case is executed server-side in the sandbox.
//
// Only after all five gates pass is the tool pushed to the tools-registry and a
// PR opened. Any failure records a guard strike so the loop can self-correct.
func (g *Generator) handleEmitTool(ctx context.Context, call llm.ToolCall, guard *safeguards.Guard, i *intent.Intent) (string, bool, error) {
	fail := func(msg string) (string, bool, error) {
		if err := guard.RecordFailure(); err != nil {
			return msg, false, err
		}
		return msg, false, nil
	}

	manifestYAML := strInput(call.Input, "manifest_yaml")
	scriptB64 := strInput(call.Input, "script_b64")
	language := strInput(call.Input, "language")
	if language == "" {
		language = "python"
	}

	// Gate 1: presence.
	if manifestYAML == "" || scriptB64 == "" {
		return fail("emit_tool: manifest_yaml and script_b64 are required")
	}

	// Gate 2: base64.
	scriptBytes, err := base64.StdEncoding.DecodeString(scriptB64)
	if err != nil {
		return fail(fmt.Sprintf("emit_tool: invalid base64 script: %v", err))
	}

	// Gate 3: manifest parse + field validation.
	m, err := tool.ParseManifestFromString(manifestYAML, string(scriptBytes), language)
	if err != nil {
		return fail(fmt.Sprintf("emit_tool: manifest invalid — fix and retry:\n%v", err))
	}

	// Gate 4: golden tests must be provided.
	rawTests, _ := call.Input["golden_tests"].([]interface{})
	if len(rawTests) < minGoldenTests {
		return fail(fmt.Sprintf(
			"emit_tool: at least %d golden_test(s) required — "+
				"add {name, input, expected_diff} entries that cover the happy path "+
				"before calling emit_tool",
			minGoldenTests,
		))
	}

	// Deserialise golden tests into typed structs.
	var goldenCases []tool.GoldenCase
	if b, _ := json.Marshal(rawTests); len(b) > 0 {
		_ = json.Unmarshal(b, &goldenCases)
	}

	// Gate 5: run all golden tests server-side.
	results := tool.RunGoldenTests(ctx, g.sandboxRunner, m, goldenCases)
	if !tool.AllPassed(results) {
		failures := strings.Join(tool.FailureMessages(results), "\n")
		return fail(fmt.Sprintf(
			"emit_tool: golden tests failed (%s) — fix the script and retry:\n%s",
			tool.SummaryLine(results), failures,
		))
	}

	guard.RecordSuccess()
	g.log.InfoContext(ctx, "emit_tool accepted — all gates passed",
		"manifest_id", m.ID,
		"golden_tests", len(goldenCases),
		"verb", i.Action.Verb,
	)

	// Push to tools-registry and open a PR if configured.
	if g.cfg.ToolsRegistryRepo != "" && g.bitbucket != nil {
		prURL, pushErr := g.pushToolPR(ctx, i, manifestYAML, string(scriptBytes), language, goldenCases)
		if pushErr != nil {
			g.log.WarnContext(ctx, "tools-registry PR failed — tool accepted but not persisted",
				"error", pushErr)
		} else {
			g.log.InfoContext(ctx, "tools-registry PR opened", "pr_url", prURL)
			return fmt.Sprintf("emit_tool: accepted — PR opened at %s", prURL), true, nil
		}
	}

	return "emit_tool: accepted (no tools-registry configured)", true, nil
}

// pushToolPR pushes the manifest, script, and golden tests to a new branch in
// the tools-registry repository and opens a pull-request for human review.
// The golden tests are committed alongside the manifest so reviewers can see
// exactly what cases were validated before the PR was opened.
func (g *Generator) pushToolPR(
	ctx context.Context,
	i *intent.Intent,
	manifestYAML, scriptContent, language string,
	goldenCases []tool.GoldenCase,
) (string, error) {
	toolDir := fmt.Sprintf("tools/%s/%s", normaliseRepoPath(i.Action.Target.Repo), i.Action.Verb)
	ext := scriptExt(language)
	branch := fmt.Sprintf("automation/new-tool/%s/%s", i.Action.Verb, i.Source.TicketID)

	targetBranch := g.cfg.ToolsRegistryBranch
	if targetBranch == "" {
		targetBranch = "main"
	}

	files := map[string]string{
		toolDir + "/manifest.yaml":  manifestYAML,
		toolDir + "/script." + ext: scriptContent,
	}

	// Commit the golden tests so human reviewers can inspect and extend them.
	if len(goldenCases) > 0 {
		if b, err := json.MarshalIndent(goldenCases, "", "  "); err == nil {
			files[toolDir+"/golden_tests.json"] = string(b)
		}
	}

	if err := g.bitbucket.PushBranch(ctx, g.cfg.ToolsRegistryRepo, branch,
		fmt.Sprintf("feat: add %s tool for %s [%s]", i.Action.Verb, i.Action.Target.Repo, i.Source.TicketID),
		files,
	); err != nil {
		return "", fmt.Errorf("push branch: %w", err)
	}

	pr, err := g.bitbucket.CreatePR(ctx, mcp.CreatePRRequest{
		Repo:         g.cfg.ToolsRegistryRepo,
		Title:        fmt.Sprintf("[%s] New tool: %s for %s", i.Source.TicketID, i.Action.Verb, i.Action.Target.Repo),
		Description:  toolPRBody(i, len(goldenCases)),
		SourceBranch: branch,
		TargetBranch: targetBranch,
	})
	if err != nil {
		return "", fmt.Errorf("create PR: %w", err)
	}
	return pr.URL, nil
}

func toolPRBody(i *intent.Intent, goldenCount int) string {
	return fmt.Sprintf(`## New Automation Tool

**Jira**: [%s](%s)
**Verb**: %s
**Target repo**: %s
**Golden tests**: %d case(s) — all passed server-side before this PR was opened.

This tool was generated automatically by OpsMate's slow-path generator.
Review the manifest, script, **and golden_tests.json** carefully before merging.
The golden tests were validated in a sandbox against the committed script; extend
them to cover edge cases before approving.

Once merged, the indexer will pick up the new tool and future tickets
triggering the same (%s, %s) pair will use it automatically.`,
		i.Source.TicketID, i.Source.URL,
		i.Action.Verb,
		i.Action.Target.Repo,
		goldenCount,
		i.Action.Verb, i.Action.Target.Repo,
	)
}

func normaliseRepoPath(repo string) string {
	return strings.ReplaceAll(repo, "/", "__")
}

func scriptExt(language string) string {
	switch language {
	case "python":
		return "py"
	case "bash":
		return "sh"
	case "go":
		return "go"
	default:
		return "sh"
	}
}

// executeParallel runs up to MaxParallelTools tool calls concurrently.
func (g *Generator) executeParallel(ctx context.Context, calls []llm.ToolCall, guard *safeguards.Guard) map[string]string {
	results := make(map[string]string, len(calls))
	type item struct {
		id     string
		result string
	}
	ch := make(chan item, len(calls))

	eg, egCtx := errgroup.WithContext(ctx)
	sem := make(chan struct{}, g.cfg.MaxParallelTools)

	for _, call := range calls {
		call := call
		// Dedup check.
		if msg := guard.CheckDuplicate(call); msg != "" {
			results[call.ID] = msg
			continue
		}
		sem <- struct{}{}
		eg.Go(func() error {
			defer func() { <-sem }()
			result := g.dispatchTool(egCtx, call)
			ch <- item{id: call.ID, result: result}
			return nil
		})
	}
	_ = eg.Wait()
	close(ch)
	for item := range ch {
		results[item.id] = item.result
	}
	return results
}

// dispatchTool routes a tool call to the appropriate Go function.
func (g *Generator) dispatchTool(ctx context.Context, call llm.ToolCall) string {
	switch call.Name {
	case "bitbucket_list_files":
		repo := strInput(call.Input, "repo")
		path := strInput(call.Input, "path")
		result, err := g.bitbucket.ListFiles(ctx, repo, path)
		if err != nil {
			return "ERROR: " + err.Error()
		}
		return truncateEntries(result, 200)

	case "bitbucket_read_file":
		repo := strInput(call.Input, "repo")
		path := strInput(call.Input, "path")
		ref := strInput(call.Input, "ref")
		result, err := g.bitbucket.GetFile(ctx, repo, path, ref)
		if err != nil {
			return "ERROR: " + err.Error()
		}
		if len(result) > 50*1024 {
			// Summarise large files via the fast model.
			return g.summarise(ctx, result, "Summarise this file for an infrastructure engineer")
		}
		return result

	case "bitbucket_search_prs":
		repo := strInput(call.Input, "repo")
		query := strInput(call.Input, "query")
		result, err := g.bitbucket.SearchPRs(ctx, repo, query)
		if err != nil {
			return "ERROR: " + err.Error()
		}
		return result

	case "bitbucket_get_pr_diff":
		repo := strInput(call.Input, "repo")
		prID := intInput(call.Input, "pr_id")
		result, err := g.bitbucket.GetPRDiff(ctx, repo, prID)
		if err != nil {
			return "ERROR: " + err.Error()
		}
		if len(result) > 30*1024 {
			return g.summarise(ctx, result, "Summarise this git diff for an infrastructure engineer")
		}
		return result

	case "registry_find_tools_with_verb":
		verb := strInput(call.Input, "verb")
		entries := g.toolReg.AllForVerb(verb)
		if len(entries) == 0 {
			return "No existing tools found for verb " + verb
		}
		var sb strings.Builder
		for _, e := range entries {
			sb.WriteString(fmt.Sprintf("- %s (%s)\n", e.Manifest.ID, e.Manifest.Repo.Pattern))
		}
		return sb.String()

	case "registry_read_tool":
		toolID := strInput(call.Input, "tool_id")
		entry, ok := g.toolReg.Get(toolID)
		if !ok {
			return "ERROR: tool not found: " + toolID
		}
		return fmt.Sprintf("=== manifest.yaml ===\n%s\n\n=== script ===\n%s",
			"(manifest YAML)", entry.Manifest.ScriptContent)

	case "sandbox_dry_run":
		return g.sandboxDryRun(ctx, call.Input)

	case "sandbox_run_golden_tests":
		return g.sandboxGoldenTests(ctx, call.Input)

	default:
		return fmt.Sprintf("ERROR: unknown tool %q", call.Name)
	}
}

func (g *Generator) sandboxDryRun(ctx context.Context, input map[string]interface{}) string {
	lang := sandbox.Language(strInput(input, "language"))
	scriptB64 := strInput(input, "script_b64")
	paramsJSON := strInput(input, "params_json")

	scriptBytes, err := base64.StdEncoding.DecodeString(scriptB64)
	if err != nil {
		return `{"ok":false,"error":"invalid base64"}`
	}

	result, err := g.sandboxRunner.Run(ctx, sandbox.Spec{
		Language:      lang,
		ScriptContent: string(scriptBytes),
		ParamsJSON:    paramsJSON,
		TimeoutSeconds: 60,
	})

	out := map[string]interface{}{
		"ok":          err == nil && result.ExitCode == 0,
		"diff":        result.Stdout,
		"stderr_tail": result.Stderr,
		"exit_code":   result.ExitCode,
	}
	if err != nil {
		out["error"] = err.Error()
	}
	b, _ := json.Marshal(out)
	return string(b)
}

func (g *Generator) sandboxGoldenTests(ctx context.Context, input map[string]interface{}) string {
	lang := strInput(input, "language")
	scriptB64 := strInput(input, "script_b64")
	scriptBytes, err := base64.StdEncoding.DecodeString(scriptB64)
	if err != nil {
		return `{"pass":0,"fail":1,"failures":["invalid base64"]}`
	}

	rawTests, _ := input["golden_tests"].([]interface{})
	var cases []tool.GoldenCase
	if b, _ := json.Marshal(rawTests); len(b) > 0 {
		_ = json.Unmarshal(b, &cases)
	}

	m := &tool.Manifest{
		Runtime:       tool.RuntimeConfig{Language: lang},
		ScriptContent: string(scriptBytes),
	}
	results := tool.RunGoldenTests(ctx, g.sandboxRunner, m, cases)

	out := map[string]interface{}{
		"pass":     0,
		"fail":     0,
		"failures": []string{},
	}
	for _, r := range results {
		if r.Passed {
			out["pass"] = out["pass"].(int) + 1
		} else {
			out["fail"] = out["fail"].(int) + 1
			out["failures"] = append(out["failures"].([]string), fmt.Sprintf("%s: %s", r.Name, r.Error))
		}
	}
	b, _ := json.Marshal(out)
	return string(b)
}

func (g *Generator) summarise(ctx context.Context, content, instruction string) string {
	resp, err := g.fast.Complete(ctx, llm.Request{
		Model:       "claude-haiku-4-5-20251001",
		Temperature: 0,
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: instruction + "\n\n" + content[:min(20000, len(content))]},
		},
	})
	if err != nil {
		return content[:min(4000, len(content))] + "\n[truncated]"
	}
	return resp.Content
}

func (g *Generator) buildTools() []llm.Tool {
	return []llm.Tool{
		{Name: "bitbucket_list_files", Description: "List files in a repo directory (max 200 entries)", InputSchema: objSchema("repo", "path")},
		{Name: "bitbucket_read_file", Description: "Read a file from a repo at a given ref", InputSchema: objSchema("repo", "path", "ref")},
		{Name: "bitbucket_search_prs", Description: "Search past PRs (BM25 over summaries)", InputSchema: objSchema("repo", "query")},
		{Name: "bitbucket_get_pr_diff", Description: "Get diff for a pull request", InputSchema: objSchema("repo", "pr_id")},
		{Name: "registry_find_tools_with_verb", Description: "Find existing tools for a verb", InputSchema: objSchema("verb")},
		{Name: "registry_read_tool", Description: "Read manifest and script for a tool", InputSchema: objSchema("tool_id")},
		{Name: "sandbox_dry_run", Description: "Run script in sandbox; returns {ok,diff,stderr_tail}", InputSchema: objSchema("language", "script_b64", "params_json")},
		{
			Name:        "sandbox_run_golden_tests",
			Description: "Run golden tests against a script; returns {pass,fail,failures}. Each golden_test must have {name, input: object, expected_diff}.",
			InputSchema: map[string]interface{}{
				"type":     "object",
				"required": []string{"language", "script_b64", "golden_tests"},
				"properties": map[string]interface{}{
					"language":   map[string]interface{}{"type": "string", "enum": []string{"python", "bash", "go"}},
					"script_b64": map[string]interface{}{"type": "string"},
					"golden_tests": map[string]interface{}{
						"type":  "array",
						"items": map[string]interface{}{"type": "object"},
					},
				},
			},
		},
		{
			Name: "emit_tool",
			Description: "Emit the final tool. The server validates the manifest, " +
				"runs all golden tests in a sandbox, and only accepts if every test passes. " +
				"Provide at least one golden_test covering the happy path before calling this.",
			InputSchema: map[string]interface{}{
				"type":     "object",
				"required": []string{"language", "script_b64", "manifest_yaml", "golden_tests"},
				"properties": map[string]interface{}{
					"language":     map[string]interface{}{"type": "string", "enum": []string{"python", "bash", "go"}},
					"script_b64":   map[string]interface{}{"type": "string", "description": "base64-encoded script content"},
					"manifest_yaml": map[string]interface{}{"type": "string", "description": "full manifest.yaml content"},
					"golden_tests": map[string]interface{}{
						"type":        "array",
						"minItems":    1,
						"description": "Test cases that were validated with sandbox_run_golden_tests before emit_tool",
						"items": map[string]interface{}{
							"type":     "object",
							"required": []string{"name", "input", "expected_diff"},
							"properties": map[string]interface{}{
								"name":          map[string]interface{}{"type": "string"},
								"input":         map[string]interface{}{"type": "object", "description": "parameters passed to the script via PARAMS_PATH"},
								"expected_diff": map[string]interface{}{"type": "string", "description": "exact unified diff the script should produce"},
							},
						},
					},
				},
			},
		},
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────

func objSchema(fields ...string) map[string]interface{} {
	props := make(map[string]interface{}, len(fields))
	for _, f := range fields {
		props[f] = map[string]interface{}{"type": "string"}
	}
	return map[string]interface{}{
		"type":       "object",
		"required":   fields,
		"properties": props,
	}
}

func strInput(m map[string]interface{}, key string) string {
	v, _ := m[key].(string)
	return v
}

func intInput(m map[string]interface{}, key string) int {
	switch v := m[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return 0
}

func mustJSON(v interface{}) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}

func truncateEntries(s string, max int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= max {
		return s
	}
	return strings.Join(lines[:max], "\n") + fmt.Sprintf("\n...%d more", len(lines)-max)
}

func normaliseWS(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
