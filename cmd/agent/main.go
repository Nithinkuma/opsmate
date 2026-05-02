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
	"github.com/nithinkuma/opsmate/pkg/config"
	"github.com/nithinkuma/opsmate/pkg/observability"
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

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))

	r.Get("/healthz", healthz(log))
	r.Get("/readyz", readyz(log))
	r.Post("/webhook/jira", jiraWebhook(log))
	r.Post("/webhook/registry", registryWebhook(log))

	srv := &http.Server{
		Addr:         ":8080",
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second,
	}

	log.Info("agent server starting", "addr", srv.Addr)
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("server error", "error", err)
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	return srv.Shutdown(shutdownCtx)
}

func healthz(log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}
}

func readyz(log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// TODO Phase 6: check DB, indexer, MCP reachability
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}
}

func jiraWebhook(log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			TicketID string `json:"ticket_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		log.InfoContext(r.Context(), "jira webhook received", "ticket_id", payload.TicketID)
		// TODO Phase 1: enqueue intent extraction
		w.WriteHeader(http.StatusAccepted)
	}
}

func registryWebhook(log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		log.InfoContext(r.Context(), "registry webhook received")
		// TODO Phase 4: trigger registry reload
		w.WriteHeader(http.StatusOK)
	}
}
