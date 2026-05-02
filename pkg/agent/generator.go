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

// handleEmitTool validates the emit_tool call, pushes the tool to the
// tools-registry repo, opens a PR, and returns (resultMsg, done, err).
func (g *Generator) handleEmitTool(ctx context.Context, call llm.ToolCall, guard *safeguards.Guard, i *intent.Intent) (string, bool, error) {
	manifestYAML := strInput(call.Input, "manifest_yaml")
	scriptB64 := strInput(call.Input, "script_b64")
	language := strInput(call.Input, "language")
	if language == "" {
		language = "python"
	}

	if manifestYAML == "" || scriptB64 == "" {
		msg := "emit_tool: manifest_yaml and script_b64 are required"
		if err := guard.RecordFailure(); err != nil {
			return msg, false, err
		}
		return msg, false, nil
	}

	scriptBytes, err := base64.StdEncoding.DecodeString(scriptB64)
	if err != nil {
		msg := fmt.Sprintf("emit_tool: invalid base64 script: %v", err)
		if err := guard.RecordFailure(); err != nil {
			return msg, false, err
		}
		return msg, false, nil
	}

	guard.RecordSuccess()
	g.log.InfoContext(ctx, "emit_tool accepted",
		"manifest_bytes", len(manifestYAML),
		"script_bytes", len(scriptBytes),
		"verb", i.Action.Verb,
	)

	// Push to tools-registry and open a PR if configured.
	if g.cfg.ToolsRegistryRepo != "" && g.bitbucket != nil {
		if prURL, pushErr := g.pushToolPR(ctx, i, manifestYAML, string(scriptBytes), language); pushErr != nil {
			g.log.WarnContext(ctx, "tools-registry PR failed — tool accepted but not persisted",
				"error", pushErr)
		} else {
			g.log.InfoContext(ctx, "tools-registry PR opened", "pr_url", prURL)
			return fmt.Sprintf("emit_tool: accepted — PR opened at %s", prURL), true, nil
		}
	}

	return "emit_tool: accepted (no tools-registry configured)", true, nil
}

// pushToolPR pushes the manifest and script to a new branch in the
// tools-registry repository and opens a pull-request for human review.
func (g *Generator) pushToolPR(ctx context.Context, i *intent.Intent, manifestYAML, scriptContent, language string) (string, error) {
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

	if err := g.bitbucket.PushBranch(ctx, g.cfg.ToolsRegistryRepo, branch,
		fmt.Sprintf("feat: add %s tool for %s [%s]", i.Action.Verb, i.Action.Target.Repo, i.Source.TicketID),
		files,
	); err != nil {
		return "", fmt.Errorf("push branch: %w", err)
	}

	pr, err := g.bitbucket.CreatePR(ctx, mcp.CreatePRRequest{
		Repo:         g.cfg.ToolsRegistryRepo,
		Title:        fmt.Sprintf("[%s] New tool: %s for %s", i.Source.TicketID, i.Action.Verb, i.Action.Target.Repo),
		Description:  toolPRBody(i),
		SourceBranch: branch,
		TargetBranch: targetBranch,
	})
	if err != nil {
		return "", fmt.Errorf("create PR: %w", err)
	}
	return pr.URL, nil
}

func toolPRBody(i *intent.Intent) string {
	return fmt.Sprintf(`## New Automation Tool

**Jira**: [%s](%s)
**Verb**: %s
**Target repo**: %s

This tool was generated automatically by OpsMate's slow-path generator.
Review the manifest and script carefully before merging.

Once merged, the indexer will pick up the new tool and future tickets
triggering the same (%s, %s) pair will use it automatically.`,
		i.Source.TicketID, i.Source.URL,
		i.Action.Verb,
		i.Action.Target.Repo,
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
	lang := sandbox.Language(strInput(input, "language"))
	scriptB64 := strInput(input, "script_b64")
	scriptBytes, err := base64.StdEncoding.DecodeString(scriptB64)
	if err != nil {
		return `{"pass":0,"fail":1,"failures":["invalid base64"]}`
	}

	rawTests, _ := input["golden_tests"].([]interface{})
	pass, fail := 0, 0
	var failures []string

	for _, rawTest := range rawTests {
		testMap, _ := rawTest.(map[string]interface{})
		paramsJSON, _ := testMap["input_json"].(string)
		expectedDiff, _ := testMap["expected_diff"].(string)
		name, _ := testMap["name"].(string)

		result, err := g.sandboxRunner.Run(ctx, sandbox.Spec{
			Language:      lang,
			ScriptContent: string(scriptBytes),
			ParamsJSON:    paramsJSON,
			TimeoutSeconds: 60,
		})
		if err != nil || result.ExitCode != 0 || normaliseWS(result.Stdout) != normaliseWS(expectedDiff) {
			fail++
			reason := fmt.Sprintf("test %q failed", name)
			if err != nil {
				reason += ": " + err.Error()
			}
			failures = append(failures, reason)
		} else {
			pass++
		}
	}

	out := map[string]interface{}{
		"pass":     pass,
		"fail":     fail,
		"failures": failures,
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
		{Name: "sandbox_run_golden_tests", Description: "Run golden tests; returns {pass,fail,failures}", InputSchema: objSchema("language", "script_b64", "golden_tests")},
		{
			Name:        "emit_tool",
			Description: "Emit the final tool. Terminates the loop if valid.",
			InputSchema: map[string]interface{}{
				"type":     "object",
				"required": []string{"language", "script_b64", "manifest_yaml"},
				"properties": map[string]interface{}{
					"language":     map[string]interface{}{"type": "string", "enum": []string{"python", "bash", "go"}},
					"script_b64":   map[string]interface{}{"type": "string", "description": "base64-encoded script"},
					"manifest_yaml": map[string]interface{}{"type": "string"},
					"golden_tests": map[string]interface{}{"type": "array"},
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
