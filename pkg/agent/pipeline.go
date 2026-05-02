package agent

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/nithinkuma/opsmate/pkg/intent"
	"github.com/nithinkuma/opsmate/pkg/llm"
	"github.com/nithinkuma/opsmate/pkg/mcp"
	"github.com/nithinkuma/opsmate/pkg/observability"
	"github.com/nithinkuma/opsmate/pkg/sandbox"
	"github.com/nithinkuma/opsmate/pkg/store"
	"github.com/nithinkuma/opsmate/pkg/tool"
	"github.com/nithinkuma/opsmate/pkg/verbs"
)

// PipelineDeps holds all injectable dependencies for the full pipeline.
type PipelineDeps struct {
	PrimaryLLM   llm.Client
	FastLLM      llm.Client
	Atlassian    *mcp.AtlassianClient
	Bitbucket    *mcp.BitbucketClient
	VerbReg      *verbs.Registry
	ToolReg      *tool.Registry
	Runner       sandbox.Runner
	DB           *store.DB
	ComponentMap ComponentMap
	PrimaryModel string
	SandboxKind  string
	GeneratorCfg GeneratorConfig
	Log          *slog.Logger
}

// PipelineResult holds the outcome of one end-to-end pipeline run.
type PipelineResult struct {
	Intent              *intent.Intent
	ResolveStatus       tool.ResolveStatus
	ExecutionID         string
	Diff                string
	PRURL               string
	Branch              string
	ClarificationNeeded string
	ToolGenerated       bool
}

// Pipeline wires the five stages: extract → resolve → (generate | execute → raise PR).
type Pipeline struct {
	deps        PipelineDeps
	intentAgent *IntentAgent
	resolver    *Resolver
	executor    *Executor
	prRaiser    *PRRaiser
	generator   *Generator
}

// NewPipeline constructs a fully wired Pipeline from its deps.
// When a DB is provided, every LLM call is traced to the llm_traces table.
func NewPipeline(d PipelineDeps) *Pipeline {
	sandboxKind := d.SandboxKind
	if sandboxKind == "" {
		sandboxKind = "local"
	}

	primaryLLM := d.PrimaryLLM
	fastLLM := d.FastLLM

	// Wrap LLM clients with tracing when Postgres is available.
	// intentID is left empty here; it will appear in traces as NULL (acceptable).
	if d.DB != nil {
		primaryLLM = store.NewTracingClient(primaryLLM, d.DB, "", "pipeline_primary")
		fastLLM = store.NewTracingClient(fastLLM, d.DB, "", "pipeline_fast")
	}

	return &Pipeline{
		deps:        d,
		intentAgent: NewIntentAgent(primaryLLM, d.Atlassian, d.VerbReg, d.DB, d.ComponentMap, d.PrimaryModel, d.Log),
		resolver:    NewResolver(d.ToolReg),
		executor:    NewExecutor(d.Runner, d.DB, d.ToolReg, sandboxKind, d.Log),
		prRaiser:    NewPRRaiser(d.Bitbucket, d.Atlassian, d.DB, d.Log),
		generator:   NewGenerator(primaryLLM, fastLLM, d.Bitbucket, d.ToolReg, d.Runner, d.GeneratorCfg, d.Log),
	}
}

// Run processes a Jira ticket through the full pipeline.
// Fast path: extract → resolve (hit) → execute → raise PR.
// Slow path: extract → resolve (miss) → generate tool → await review.
func (p *Pipeline) Run(ctx context.Context, ticketID string) (*PipelineResult, error) {
	ctx, span := observability.Start(ctx, "pipeline.run")
	defer span.End()

	// Stage 1: extract intent
	extracted, err := p.intentAgent.Extract(ctx, ticketID)
	if err != nil {
		return nil, fmt.Errorf("pipeline[%s] intent: %w", ticketID, err)
	}
	if extracted.ClarificationNeeded != "" {
		return &PipelineResult{ClarificationNeeded: extracted.ClarificationNeeded}, nil
	}

	i := extracted.Intent
	result := &PipelineResult{Intent: i}
	ctx = observability.WithIntentID(ctx, i.IntentID)

	// Stage 2: resolve tool
	resolved := p.resolver.Resolve(i)
	result.ResolveStatus = resolved.Status

	// Slow path: cache miss → generate a new tool
	if resolved.Status == tool.ResolveNotFound {
		p.deps.Log.InfoContext(ctx, "cache miss — invoking generator",
			"verb", i.Action.Verb, "repo", i.Action.Target.Repo)
		toolPR, err := p.generator.Generate(ctx, i)
		if err != nil {
			return nil, fmt.Errorf("pipeline[%s] generator: %w", ticketID, err)
		}
		result.ToolGenerated = true
		_ = toolPR
		return result, nil
	}

	// Fast path: execute existing tool in sandbox
	execResult, err := p.executor.Execute(ctx, i, resolved.Entry)
	if err != nil {
		return nil, fmt.Errorf("pipeline[%s] execute: %w", ticketID, err)
	}
	result.ExecutionID = execResult.ExecutionID
	result.Diff = execResult.Diff

	// Stage 4: raise PR + save summary for future BM25 search
	raiseResult, err := p.prRaiser.Raise(ctx, i, resolved.Entry, execResult)
	if err != nil {
		return nil, fmt.Errorf("pipeline[%s] pr_raise: %w", ticketID, err)
	}
	result.PRURL = raiseResult.PRURL
	result.Branch = raiseResult.Branch

	if p.deps.DB != nil && result.PRURL != "" {
		_ = p.deps.DB.SavePRSummary(ctx, &store.PRSummary{
			Repo:    i.Action.Target.Repo,
			Verb:    i.Action.Verb,
			PRURL:   result.PRURL,
			Title:   fmt.Sprintf("[%s] %s", i.Source.TicketID, i.Action.Verb),
			Summary: i.Context.DescriptionRaw,
		})
	}

	return result, nil
}
