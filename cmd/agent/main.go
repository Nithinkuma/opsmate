// cmd/agent is the single control-plane binary.
// It exposes a REST API, handles Jira and registry webhooks, and runs the
// background indexer so only one process is needed in development.
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/nithinkuma/opsmate/pkg/agent"
	"github.com/nithinkuma/opsmate/pkg/config"
	"github.com/nithinkuma/opsmate/pkg/eval"
	"github.com/nithinkuma/opsmate/pkg/llm"
	"github.com/nithinkuma/opsmate/pkg/mcp"
	"github.com/nithinkuma/opsmate/pkg/observability"
	"github.com/nithinkuma/opsmate/pkg/policy"
	"github.com/nithinkuma/opsmate/pkg/sandbox"
	"github.com/nithinkuma/opsmate/pkg/store"
	"github.com/nithinkuma/opsmate/pkg/tool"
	"github.com/nithinkuma/opsmate/pkg/verbs"
	"github.com/spf13/cobra"
)

func main() {
	var cfgFile string
	root := &cobra.Command{
		Use:   "agent",
		Short: "Jira-to-PR agent server",
		RunE:  func(cmd *cobra.Command, _ []string) error { return runServer(cfgFile) },
	}
	root.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default ./config.yaml)")
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

// srv bundles long-lived dependencies shared by all HTTP handlers.
type srv struct {
	log      *slog.Logger
	cfg      *config.Config
	pipeline *agent.Pipeline
	toolReg  *tool.Registry
	toolSrc  tool.Source
	db       *store.DB
}

func runServer(cfgFile string) error {
	cfg, err := config.Load(cfgFile)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	log := observability.NewLogger(cfg.Observability.LogLevel)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	shutdown, err := observability.Setup(ctx, observability.Config{
		OTLPEndpoint: cfg.Observability.OTLPEndpoint,
		LogLevel:     cfg.Observability.LogLevel,
	})
	if err != nil {
		return fmt.Errorf("otel setup: %w", err)
	}
	defer func() { _ = shutdown(context.Background()) }()

	// ── Dependencies ──────────────────────────────────────────────────────────
	primaryLLM, err := llm.New(cfg.LLM.Primary)
	if err != nil {
		return fmt.Errorf("primary llm: %w", err)
	}
	fastLLM, err := llm.New(cfg.LLM.Fast)
	if err != nil {
		return fmt.Errorf("fast llm: %w", err)
	}

	atlassian, err := mcp.NewAtlassianClient(cfg.MCP.Atlassian.URL, cfg.MCP.Atlassian.Auth)
	if err != nil {
		return fmt.Errorf("atlassian client: %w", err)
	}
	bitbucket, err := mcp.NewBitbucketClient(cfg.MCP.Bitbucket.URL, cfg.MCP.Bitbucket.Auth)
	if err != nil {
		return fmt.Errorf("bitbucket client: %w", err)
	}

	verbReg, err := verbs.LoadSeed()
	if err != nil {
		return fmt.Errorf("verb registry: %w", err)
	}

	toolReg := tool.NewRegistry()
	src := toolSource(cfg)

	var db *store.DB
	if cfg.Postgres.DSN != "" {
		db, err = store.Open(ctx, cfg.Postgres.DSN)
		if err != nil {
			log.Warn("postgres unavailable — running without persistence", "error", err)
		} else {
			defer db.Close()
			log.Info("postgres connected")
		}
	}

	// Initial index (non-fatal if it fails).
	if err := indexTools(ctx, log, src, toolReg, db); err != nil {
		log.Warn("initial tool index failed", "error", err)
	}

	runner, err := buildRunner(cfg, log)
	if err != nil {
		return err
	}

	maxSteps := cfg.Agent.GeneratorMaxSteps
	if maxSteps == 0 {
		maxSteps = 15
	}
	maxParallel := cfg.Agent.GeneratorMaxParallelTools
	if maxParallel == 0 {
		maxParallel = 4
	}

	pipeline := agent.NewPipeline(agent.PipelineDeps{
		PrimaryLLM:   primaryLLM,
		FastLLM:      fastLLM,
		Atlassian:    atlassian,
		Bitbucket:    bitbucket,
		VerbReg:      verbReg,
		ToolReg:      toolReg,
		Runner:       runner,
		DB:           db,
		PrimaryModel: cfg.LLM.Primary.Model,
		SandboxKind:  cfg.Sandbox.Kind,
		GeneratorCfg: agent.GeneratorConfig{
			MaxSteps:            maxSteps,
			MaxParallelTools:    maxParallel,
			Temperature:         cfg.Agent.GeneratorTemperature,
			Model:               cfg.LLM.Primary.Model,
			ToolsRegistryRepo:   cfg.ToolsRegistry.GitURL,
			ToolsRegistryBranch: cfg.ToolsRegistry.Branch,
		},
		Log: log,
	})

	s := &srv{log: log, cfg: cfg, pipeline: pipeline, toolReg: toolReg, toolSrc: src, db: db}

	// ── Background indexer ────────────────────────────────────────────────────
	go s.runIndexer(ctx, toolReg, db)

	// ── HTTP router ───────────────────────────────────────────────────────────
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(120 * time.Second))

	// Health
	r.Get("/healthz", s.handleHealthz)
	r.Get("/readyz", s.handleReadyz)

	// Webhooks (Jira / registry push)
	r.Post("/webhook/jira", s.handleJiraWebhook)
	r.Post("/webhook/registry", s.handleRegistryWebhook)

	// REST API v1
	r.Route("/api/v1", func(r chi.Router) {
		// Ticket processing
		r.Post("/tickets/process", s.handleProcessTicket)

		// Tool registry
		r.Get("/tools", s.handleListTools)
		r.Get("/tools/{toolID}", s.handleGetTool)
		r.Post("/tools/{toolID}/promote", s.handlePromoteTool)
		r.Post("/tools/validate", s.handleValidateTool)

		// Intents & executions (read-only, DB required)
		r.Get("/intents/{id}", s.handleGetIntent)
		r.Get("/executions/{id}", s.handleGetExecution)

		// Eval
		r.Post("/eval", s.handleEval)

		// PR summary search
		r.Get("/pr-summaries/search", s.handleSearchPRSummaries)
	})

	httpSrv := &http.Server{
		Addr:         ":8080",
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 120 * time.Second,
	}

	log.Info("agent server started", "addr", httpSrv.Addr)
	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("server error", "error", err)
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutCancel()
	return httpSrv.Shutdown(shutCtx)
}

// ── Health ────────────────────────────────────────────────────────────────────

func (s *srv) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *srv) handleReadyz(w http.ResponseWriter, r *http.Request) {
	code := http.StatusOK
	status := "ok"
	if s.toolReg.Len() == 0 {
		code = http.StatusServiceUnavailable
		status = "degraded: tool registry empty"
	}
	writeJSON(w, code, map[string]interface{}{"status": status, "tool_count": s.toolReg.Len()})
}

// ── POST /api/v1/tickets/process ─────────────────────────────────────────────

func (s *srv) handleProcessTicket(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TicketID string `json:"ticket_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.TicketID == "" {
		writeError(w, http.StatusBadRequest, "ticket_id is required")
		return
	}
	ticketID := body.TicketID

	s.log.InfoContext(r.Context(), "ticket processing requested", "ticket_id", ticketID)

	// Fire pipeline asynchronously; caller gets 202 immediately.
	go func() {
		runCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		result, err := s.pipeline.Run(runCtx, ticketID)
		if err != nil {
			s.log.Error("pipeline failed", "ticket_id", ticketID, "error", err)
			return
		}
		if result.ClarificationNeeded != "" {
			s.log.Info("clarification needed", "ticket_id", ticketID, "message", result.ClarificationNeeded)
			return
		}
		if result.ToolGenerated {
			s.log.Info("new tool generated", "ticket_id", ticketID, "verb", result.Intent.Action.Verb)
			return
		}
		s.log.Info("PR raised", "ticket_id", ticketID, "pr_url", result.PRURL, "branch", result.Branch)
	}()

	writeJSON(w, http.StatusAccepted, map[string]string{
		"status":    "accepted",
		"ticket_id": ticketID,
	})
}

// ── POST /webhook/jira ────────────────────────────────────────────────────────

func (s *srv) handleJiraWebhook(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		TicketID string `json:"ticket_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload.TicketID == "" {
		writeError(w, http.StatusBadRequest, "missing ticket_id")
		return
	}
	go s.runPipelineAsync(payload.TicketID)
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted", "ticket_id": payload.TicketID})
}

// ── POST /webhook/registry ────────────────────────────────────────────────────

func (s *srv) handleRegistryWebhook(w http.ResponseWriter, r *http.Request) {
	s.log.InfoContext(r.Context(), "registry webhook — triggering reload")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := indexTools(ctx, s.log, s.toolSrc, s.toolReg, s.db); err != nil {
			s.log.Error("registry reload failed", "error", err)
		}
	}()
	w.WriteHeader(http.StatusAccepted)
}

// ── GET /api/v1/tools ─────────────────────────────────────────────────────────

func (s *srv) handleListTools(w http.ResponseWriter, r *http.Request) {
	verb := r.URL.Query().Get("verb")

	type toolSummary struct {
		ID          string `json:"id"`
		Verb        string `json:"verb"`
		RepoPattern string `json:"repo_pattern"`
		Language    string `json:"language"`
		PolicyState string `json:"policy_state"`
	}

	var entries []*tool.Entry
	if verb != "" {
		entries = s.toolReg.AllForVerb(verb)
	} else {
		entries = s.toolReg.All()
	}

	out := make([]toolSummary, len(entries))
	for i, e := range entries {
		out[i] = toolSummary{
			ID:          e.Manifest.ID,
			Verb:        e.Manifest.Verb,
			RepoPattern: e.Manifest.Repo.Pattern,
			Language:    e.Manifest.Runtime.Language,
			PolicyState: string(e.PolicyState),
		}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"tools": out, "count": len(out)})
}

// ── GET /api/v1/tools/{toolID} ────────────────────────────────────────────────

func (s *srv) handleGetTool(w http.ResponseWriter, r *http.Request) {
	toolID := chi.URLParam(r, "toolID")
	entry, ok := s.toolReg.Get(toolID)
	if !ok {
		writeError(w, http.StatusNotFound, "tool not found: "+toolID)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":           entry.Manifest.ID,
		"verb":         entry.Manifest.Verb,
		"repo_pattern": entry.Manifest.Repo.Pattern,
		"language":     entry.Manifest.Runtime.Language,
		"policy_state": string(entry.PolicyState),
		"hash":         entry.Manifest.Hash,
	})
}

// ── POST /api/v1/tools/validate ──────────────────────────────────────────────
// Stateless endpoint: parse a manifest + run golden tests without touching the
// registry or DB. Useful from CI pipelines validating a tools-registry PR.

func (s *srv) handleValidateTool(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ManifestYAML string           `json:"manifest_yaml"`
		ScriptB64    string           `json:"script_b64"`
		Language     string           `json:"language"`
		GoldenTests  []tool.GoldenCase `json:"golden_tests"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.ManifestYAML == "" || body.ScriptB64 == "" {
		writeError(w, http.StatusBadRequest, "manifest_yaml and script_b64 are required")
		return
	}

	scriptBytes, err := base64.StdEncoding.DecodeString(body.ScriptB64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "script_b64: invalid base64")
		return
	}

	lang := body.Language
	if lang == "" {
		lang = "python"
	}

	m, parseErr := tool.ParseManifestFromString(body.ManifestYAML, string(scriptBytes), lang)
	if parseErr != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]interface{}{
			"valid":           false,
			"manifest_errors": []string{parseErr.Error()},
			"test_results":    nil,
		})
		return
	}

	results := tool.RunGoldenTests(r.Context(), sandbox.NewLocalRunner(s.log), m, body.GoldenTests)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"valid":           tool.AllPassed(results),
		"summary":         tool.SummaryLine(results),
		"manifest_errors": nil,
		"test_results":    results,
	})
}

// ── POST /api/v1/tools/{toolID}/promote ──────────────────────────────────────

func (s *srv) handlePromoteTool(w http.ResponseWriter, r *http.Request) {
	toolID := chi.URLParam(r, "toolID")

	var body struct {
		State string `json:"state"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	newState := policy.State(body.State)
	switch newState {
	case policy.StateReview, policy.StateAutoMergeEligible, policy.StateAutoMergeActive:
	default:
		writeError(w, http.StatusBadRequest, "state must be review|auto_merge_eligible|auto_merge_active")
		return
	}

	if err := s.toolReg.SetPolicyState(toolID, newState); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	if s.db != nil {
		if err := s.db.SetToolPolicyState(r.Context(), toolID, newState); err != nil {
			s.log.Warn("failed to persist policy state", "tool_id", toolID, "error", err)
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"tool_id": toolID,
		"state":   string(newState),
	})
}

// ── GET /api/v1/intents/{id} ──────────────────────────────────────────────────

func (s *srv) handleGetIntent(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writeError(w, http.StatusServiceUnavailable, "postgres not configured")
		return
	}
	id := chi.URLParam(r, "id")
	row, err := s.db.GetIntent(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, row)
}

// ── GET /api/v1/executions/{id} ───────────────────────────────────────────────

func (s *srv) handleGetExecution(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writeError(w, http.StatusServiceUnavailable, "postgres not configured")
		return
	}
	id := chi.URLParam(r, "id")
	row, err := s.db.GetExecution(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, row)
}

// ── POST /api/v1/eval ─────────────────────────────────────────────────────────

func (s *srv) handleEval(w http.ResponseWriter, r *http.Request) {
	var body struct {
		GoldenDir string `json:"golden_dir"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		body.GoldenDir = ""
	}
	if body.GoldenDir == "" {
		body.GoldenDir = "eval/golden"
	}

	cases, err := eval.LoadCases(body.GoldenDir)
	if err != nil {
		writeError(w, http.StatusBadRequest, "load cases: "+err.Error())
		return
	}

	runner := eval.NewRunner(s.toolReg, sandbox.NewLocalRunner(s.log))
	results := runner.RunAll(r.Context(), cases)

	type resultJSON struct {
		Name     string `json:"name"`
		Passed   bool   `json:"passed"`
		Error    string `json:"error,omitempty"`
		DurationMs int64 `json:"duration_ms"`
	}
	out := make([]resultJSON, len(results))
	pass, fail := 0, 0
	for i, res := range results {
		out[i] = resultJSON{
			Name:       res.Name,
			Passed:     res.Passed,
			Error:      res.Error,
			DurationMs: res.Duration.Milliseconds(),
		}
		if res.Passed && res.Error == "" {
			pass++
		} else {
			fail++
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"pass":    pass,
		"fail":    fail,
		"total":   len(results),
		"results": out,
		"summary": eval.Summary(results),
	})
}

// ── GET /api/v1/pr-summaries/search ──────────────────────────────────────────

func (s *srv) handleSearchPRSummaries(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writeError(w, http.StatusServiceUnavailable, "postgres not configured")
		return
	}
	q := r.URL.Query().Get("q")
	if q == "" {
		writeError(w, http.StatusBadRequest, "q query param required")
		return
	}
	limit := 10
	results, err := s.db.SearchPRSummaries(r.Context(), q, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"results": results, "count": len(results)})
}

// ── Background indexer (embedded in server) ───────────────────────────────────

func (s *srv) runIndexer(ctx context.Context, reg *tool.Registry, db *store.DB) {
	interval, err := time.ParseDuration(s.cfg.ToolsRegistry.PollInterval)
	if err != nil || interval == 0 {
		interval = 60 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := indexTools(ctx, s.log, s.toolSrc, reg, db); err != nil {
				s.log.Warn("indexer poll failed", "error", err)
			}
		}
	}
}

func (s *srv) runPipelineAsync(ticketID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	result, err := s.pipeline.Run(ctx, ticketID)
	if err != nil {
		s.log.Error("pipeline failed", "ticket_id", ticketID, "error", err)
		return
	}
	if result.ClarificationNeeded != "" {
		s.log.Info("clarification needed", "ticket_id", ticketID)
		return
	}
	if result.ToolGenerated {
		s.log.Info("new tool generated", "ticket_id", ticketID)
		return
	}
	s.log.Info("PR raised", "ticket_id", ticketID, "pr_url", result.PRURL)
}

// ── Shared helpers ────────────────────────────────────────────────────────────

func indexTools(ctx context.Context, log *slog.Logger, src tool.Source, reg *tool.Registry, db *store.DB) error {
	manifests, err := src.Load(ctx)
	if err != nil {
		return fmt.Errorf("load manifests: %w", err)
	}
	loaded, skipped := 0, 0
	for _, m := range manifests {
		if err := reg.Register(m); err != nil {
			log.Error("skipping tool", "id", m.ID, "error", err)
			skipped++
			continue
		}
		if db != nil {
			if state, dbErr := db.GetToolPolicyState(ctx, m.ID); dbErr == nil {
				_ = reg.SetPolicyState(m.ID, state)
			} else {
				_ = db.UpsertToolIndex(ctx, m, policy.StateReview)
			}
		}
		loaded++
	}
	log.Info("tool index refreshed", "loaded", loaded, "skipped", skipped, "total", reg.Len())
	return nil
}

func toolSource(cfg *config.Config) tool.Source {
	if cfg.ToolsRegistry.GitURL != "" {
		return tool.NewGitSource(cfg.ToolsRegistry.GitURL, cfg.ToolsRegistry.Branch)
	}
	seedPath := cfg.ToolsRegistry.SeedPath
	if seedPath == "" {
		seedPath = "./tools-registry-seed"
	}
	return tool.NewLocalSource(seedPath)
}

func buildRunner(cfg *config.Config, log *slog.Logger) (sandbox.Runner, error) {
	if cfg.Sandbox.Kind == "k8s_job" {
		ns := cfg.Sandbox.Namespace
		if ns == "" {
			ns = "agent-sandbox"
		}
		return sandbox.NewK8sJobRunner(ns, log)
	}
	return sandbox.NewLocalRunner(log), nil
}

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
