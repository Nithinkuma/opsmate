// cmd/agent is the main control-plane binary: HTTP server + background worker.
package main

import (
	"context"
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
	"github.com/nithinkuma/opsmate/pkg/llm"
	"github.com/nithinkuma/opsmate/pkg/mcp"
	"github.com/nithinkuma/opsmate/pkg/observability"
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
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runServer(cfgFile)
		},
	}
	root.PersistentFlags().StringVar(&cfgFile, "config", "", "config file")
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

// srv bundles all long-lived dependencies so they can be shared across handlers.
type srv struct {
	log      *slog.Logger
	pipeline *agent.Pipeline
	db       *store.DB
	toolReg  *tool.Registry
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

	// ── Build dependencies ────────────────────────────────────────────────────

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
	if manifests, loadErr := src.Load(ctx); loadErr != nil {
		log.Warn("initial tool registry load failed", "error", loadErr)
	} else {
		for _, m := range manifests {
			if regErr := toolReg.Register(m); regErr != nil {
				log.Warn("skipping tool", "id", m.ID, "error", regErr)
			}
		}
		log.Info("tool registry loaded", "count", toolReg.Len())
	}

	runner, err := buildRunner(cfg, log)
	if err != nil {
		return err
	}

	var db *store.DB
	if cfg.Postgres.DSN != "" {
		db, err = store.Open(ctx, cfg.Postgres.DSN)
		if err != nil {
			log.Warn("postgres unavailable — running without persistence", "error", err)
		} else {
			defer db.Close()
		}
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
			MaxSteps:         maxSteps,
			MaxParallelTools: maxParallel,
			Temperature:      cfg.Agent.GeneratorTemperature,
			Model:            cfg.LLM.Primary.Model,
		},
		Log: log,
	})

	s := &srv{log: log, pipeline: pipeline, db: db, toolReg: toolReg}

	// ── HTTP router ───────────────────────────────────────────────────────────

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))

	r.Get("/healthz", s.healthz)
	r.Get("/readyz", s.readyz)
	r.Post("/webhook/jira", s.jiraWebhook)
	r.Post("/webhook/registry", s.registryWebhook(src, toolReg))

	httpSrv := &http.Server{
		Addr:         ":8080",
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second,
	}

	log.Info("agent server starting", "addr", httpSrv.Addr)
	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("server error", "error", err)
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	return httpSrv.Shutdown(shutdownCtx)
}

func (s *srv) healthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *srv) readyz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	status := "ok"
	code := http.StatusOK
	if s.toolReg.Len() == 0 {
		status = "degraded: tool registry empty"
		code = http.StatusServiceUnavailable
	}
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": status, "tools": fmt.Sprint(s.toolReg.Len())})
}

func (s *srv) jiraWebhook(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		TicketID string `json:"ticket_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload.TicketID == "" {
		http.Error(w, "missing ticket_id", http.StatusBadRequest)
		return
	}

	ticketID := payload.TicketID
	s.log.InfoContext(r.Context(), "jira webhook received", "ticket_id", ticketID)

	// Fire pipeline asynchronously; the webhook returns immediately.
	go func() {
		runCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		result, err := s.pipeline.Run(runCtx, ticketID)
		if err != nil {
			s.log.Error("pipeline failed", "ticket_id", ticketID, "error", err)
			return
		}
		if result.ClarificationNeeded != "" {
			s.log.Info("pipeline: clarification needed", "ticket_id", ticketID, "message", result.ClarificationNeeded)
			return
		}
		if result.ToolGenerated {
			s.log.Info("pipeline: new tool generated — awaiting review", "ticket_id", ticketID)
			return
		}
		s.log.Info("pipeline: PR raised", "ticket_id", ticketID, "pr_url", result.PRURL)
	}()

	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "accepted", "ticket_id": ticketID})
}

func (s *srv) registryWebhook(src tool.Source, reg *tool.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.log.InfoContext(r.Context(), "registry webhook received — reloading")
		go func() {
			reloadCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()

			manifests, err := src.Load(reloadCtx)
			if err != nil {
				s.log.Error("registry reload failed", "error", err)
				return
			}
			loaded, skipped := 0, 0
			for _, m := range manifests {
				if err := reg.Register(m); err != nil {
					s.log.Warn("skipping tool", "id", m.ID, "error", err)
					skipped++
				} else {
					loaded++
				}
			}
			s.log.Info("registry reloaded", "loaded", loaded, "skipped", skipped, "total", reg.Len())
		}()
		w.WriteHeader(http.StatusAccepted)
	}
}

// ── helpers (shared with cmd/cli via duplication — kept simple) ──────────────

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
