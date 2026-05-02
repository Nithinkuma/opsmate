// Package agent contains all five pipeline stages.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"text/template"
	"time"

	_ "embed"

	"github.com/nithinkuma/opsmate/pkg/intent"
	"github.com/nithinkuma/opsmate/pkg/llm"
	"github.com/nithinkuma/opsmate/pkg/mcp"
	"github.com/nithinkuma/opsmate/pkg/observability"
	"github.com/nithinkuma/opsmate/pkg/store"
	"github.com/nithinkuma/opsmate/pkg/verbs"
	"github.com/oklog/ulid/v2"
)

//go:embed prompts/intent.system.md
var intentSystemPromptTemplate string

// ComponentMap maps Jira component names to git repos. Loaded from config.
type ComponentMap map[string]string

// IntentAgent extracts a structured Intent from a Jira ticket using one LLM call.
type IntentAgent struct {
	llm          llm.Client
	atlassian    *mcp.AtlassianClient
	verbRegistry *verbs.Registry
	db           *store.DB
	componentMap ComponentMap
	model        string
	log          *slog.Logger
}

// NewIntentAgent constructs the agent with all required dependencies.
func NewIntentAgent(
	llmClient llm.Client,
	atlassian *mcp.AtlassianClient,
	verbRegistry *verbs.Registry,
	db *store.DB,
	componentMap ComponentMap,
	model string,
	log *slog.Logger,
) *IntentAgent {
	return &IntentAgent{
		llm:          llmClient,
		atlassian:    atlassian,
		verbRegistry: verbRegistry,
		db:           db,
		componentMap: componentMap,
		model:        model,
		log:          log,
	}
}

// ExtractResult is the outcome of one intent extraction attempt.
type ExtractResult struct {
	Intent *intent.Intent
	// ClarificationNeeded is set when the LLM could not map the ticket to a verb.
	ClarificationNeeded string
}

// Extract fetches the ticket, calls the LLM with emit_intent, validates the
// result, persists it, and returns. On any validation failure it comments back
// to Jira and returns an error.
func (a *IntentAgent) Extract(ctx context.Context, ticketID string) (*ExtractResult, error) {
	ctx, span := observability.Start(ctx, "intent_agent.extract")
	defer span.End()

	// ── 1. Fetch ticket ──────────────────────────────────────────────────────
	ticket, err := a.atlassian.GetTicket(ctx, ticketID)
	if err != nil {
		return nil, fmt.Errorf("intent: fetch ticket %s: %w", ticketID, err)
	}

	// ── 2. Resolve repo ──────────────────────────────────────────────────────
	repo, resolutionMethod := a.resolveRepo(ticket)

	// ── 3. Build constrained system prompt ──────────────────────────────────
	systemPrompt, err := a.buildSystemPrompt()
	if err != nil {
		return nil, fmt.Errorf("intent: build prompt: %w", err)
	}

	// ── 4. Build emit_intent tool definition ─────────────────────────────────
	emitTool := a.buildEmitIntentTool()

	userMsg := fmt.Sprintf(
		"Jira ticket: %s\nSummary: %s\n\nDescription:\n%s\n\nPriority: %s\nLinked tickets: %s",
		ticket.Key, ticket.Summary, ticket.Description,
		ticket.Priority, strings.Join(ticket.LinkedKeys, ", "),
	)
	if repo != "" {
		userMsg += "\n\nRepo hint: " + repo
	}

	req := llm.Request{
		Model:       a.model,
		Temperature: 0,
		ToolChoice:  llm.ToolChoiceRequired,
		Tools:       []llm.Tool{emitTool},
		System:      systemPrompt,
		Messages:    []llm.Message{{Role: llm.RoleUser, Content: userMsg}},
	}

	// ── 5. Call LLM ──────────────────────────────────────────────────────────
	start := time.Now()
	resp, err := a.llm.Complete(ctx, req)
	latency := int(time.Since(start).Milliseconds())
	a.log.InfoContext(ctx, "llm_call",
		"operation", "intent_extraction",
		"model", a.model,
		"input_tokens", resp.Usage.InputTokens,
		"output_tokens", resp.Usage.OutputTokens,
		"cost_usd", resp.Usage.CostUSD,
		"latency_ms", latency,
	)
	if err != nil {
		return nil, fmt.Errorf("intent: llm call: %w", err)
	}

	if len(resp.ToolCalls) == 0 {
		msg := "Intent extraction produced no tool call — this is a system error."
		_ = a.atlassian.AddComment(ctx, ticketID, msg)
		return nil, fmt.Errorf("intent: no tool call in response")
	}

	call := resp.ToolCalls[0]
	if call.Name != "emit_intent" {
		_ = a.atlassian.AddComment(ctx, ticketID,
			fmt.Sprintf("Unexpected tool call %q — expected emit_intent.", call.Name))
		return nil, fmt.Errorf("intent: unexpected tool call %q", call.Name)
	}

	// ── 6. Handle clarification request ──────────────────────────────────────
	if verb, _ := call.Input["verb"].(string); verb == "__clarification_needed__" {
		msg, _ := call.Input["clarification_message"].(string)
		_ = a.atlassian.AddComment(ctx, ticketID,
			"OpsMate needs clarification before processing this ticket:\n\n"+msg)
		return &ExtractResult{ClarificationNeeded: msg}, nil
	}

	// ── 7. Build and validate the Intent ─────────────────────────────────────
	i, err := a.buildIntent(ticket, call.Input, repo, resolutionMethod)
	if err != nil {
		errMsg := fmt.Sprintf("OpsMate could not process this ticket due to a validation error:\n\n```\n%v\n```\n\nPlease correct the ticket and re-trigger.", err)
		_ = a.atlassian.AddComment(ctx, ticketID, errMsg)
		return nil, fmt.Errorf("intent: build: %w", err)
	}

	if err := intent.Validate(i); err != nil {
		_ = a.atlassian.AddComment(ctx, ticketID,
			fmt.Sprintf("Intent schema validation failed:\n\n```\n%v\n```", err))
		return nil, fmt.Errorf("intent: validate: %w", err)
	}

	// Validate verb parameters.
	verb, _ := call.Input["verb"].(string)
	if err := a.verbRegistry.Validate(verb, i.Action.Parameters); err != nil {
		_ = a.atlassian.AddComment(ctx, ticketID,
			fmt.Sprintf("Parameter validation failed for verb %q:\n\n```\n%v\n```", verb, err))
		return nil, fmt.Errorf("intent: param validation: %w", err)
	}

	// ── 8. Persist ───────────────────────────────────────────────────────────
	ctx = observability.WithIntentID(ctx, i.IntentID)
	if a.db != nil {
		if err := a.db.SaveIntent(ctx, i); err != nil {
			a.log.WarnContext(ctx, "failed to persist intent", "error", err)
		}
	}

	a.log.InfoContext(ctx, "intent extracted",
		"intent_id", i.IntentID,
		"verb", i.Action.Verb,
		"repo", i.Action.Target.Repo,
	)

	return &ExtractResult{Intent: i}, nil
}

func (a *IntentAgent) resolveRepo(ticket *mcp.JiraTicket) (repo, method string) {
	// Tier 1: explicit repo field in the ticket URL / custom field
	// (not available in the basic JiraTicket struct — would need a custom field)

	// Tier 2: component name → repo map
	for _, comp := range ticket.Components {
		if r, ok := a.componentMap[comp]; ok {
			return r, "component_map"
		}
	}

	// Tier 3: LLM-resolved (flagged for auditability; resolution happens in buildIntent)
	return "", "llm"
}

func (a *IntentAgent) buildIntent(
	ticket *mcp.JiraTicket,
	input map[string]interface{},
	repoHint, resolutionMethod string,
) (*intent.Intent, error) {
	verb, _ := input["verb"].(string)
	repo, _ := input["repo"].(string)
	if repo == "" {
		repo = repoHint
	}
	if repo == "" {
		return nil, fmt.Errorf("could not determine target repository")
	}
	branch, _ := input["branch"].(string)
	if branch == "" {
		branch = "main"
	}
	scope, _ := input["scope"].(string)
	params, _ := input["parameters"].(map[string]interface{})
	if params == nil {
		params = make(map[string]interface{})
	}

	id := ulid.Make().String()
	i := &intent.Intent{
		IntentID:      id,
		SchemaVersion: intent.SchemaVersion,
		Source: intent.Source{
			System:               "jira",
			TicketID:             ticket.Key,
			URL:                  ticket.URL,
			Reporter:             ticket.Reporter,
			FetchedAt:            time.Now().UTC(),
			RepoResolutionMethod: resolutionMethod,
		},
		Action: intent.Action{
			Verb: verb,
			Target: intent.Target{
				Repo:   repo,
				Branch: branch,
				Scope:  scope,
			},
			Parameters: params,
		},
		Context: intent.Context{
			DescriptionRaw: ticket.Description,
			LinkedTickets:  ticket.LinkedKeys,
			Priority:       ticket.Priority,
		},
		Policy: intent.Policy{
			AutoMerge:            false,
			RequireHumanApproval: true,
		},
	}
	return i, nil
}

func (a *IntentAgent) buildSystemPrompt() (string, error) {
	knownVerbs := a.verbRegistry.KnownVerbs()
	verbJSON, _ := json.Marshal(knownVerbs)

	var verbList strings.Builder
	for _, v := range knownVerbs {
		verbList.WriteString("- `" + v + "`\n")
	}

	var compMap strings.Builder
	for comp, repo := range a.componentMap {
		compMap.WriteString(fmt.Sprintf("- %s → %s\n", comp, repo))
	}
	if compMap.Len() == 0 {
		compMap.WriteString("(none configured)\n")
	}

	tmpl, err := template.New("intent_system").Parse(intentSystemPromptTemplate)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	err = tmpl.Execute(&buf, map[string]interface{}{
		"VerbList":     verbList.String(),
		"VerbEnum":     string(verbJSON),
		"ComponentMap": compMap.String(),
	})
	return buf.String(), err
}

func (a *IntentAgent) buildEmitIntentTool() llm.Tool {
	knownVerbs := a.verbRegistry.KnownVerbs()
	verbEnumRaw := make([]interface{}, len(knownVerbs)+1)
	verbEnumRaw[0] = "__clarification_needed__"
	for i, v := range knownVerbs {
		verbEnumRaw[i+1] = v
	}

	return llm.Tool{
		Name:        "emit_intent",
		Description: "Emit a structured Intent. Call this exactly once.",
		InputSchema: map[string]interface{}{
			"type":     "object",
			"required": []string{"verb", "repo", "branch", "parameters"},
			"properties": map[string]interface{}{
				"verb":   map[string]interface{}{"type": "string", "enum": verbEnumRaw},
				"repo":   map[string]interface{}{"type": "string"},
				"branch": map[string]interface{}{"type": "string"},
				"scope":  map[string]interface{}{"type": "string"},
				"parameters": map[string]interface{}{
					"type": "object",
					"description": "Verb-specific parameters matching the verb's JSON Schema",
				},
				"clarification_message": map[string]interface{}{"type": "string"},
			},
		},
	}
}
