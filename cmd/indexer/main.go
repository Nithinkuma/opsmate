// cmd/indexer watches the tools-registry git repo, validates manifests,
// and populates the in-memory + Postgres tool index.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nithinkuma/opsmate/pkg/config"
	"github.com/nithinkuma/opsmate/pkg/observability"
	"github.com/nithinkuma/opsmate/pkg/policy"
	"github.com/nithinkuma/opsmate/pkg/store"
	"github.com/nithinkuma/opsmate/pkg/tool"
	"github.com/spf13/cobra"
)

func main() {
	var cfgFile string
	root := &cobra.Command{
		Use:   "indexer",
		Short: "tools-registry indexer service",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runIndexer(cfgFile)
		},
	}
	root.PersistentFlags().StringVar(&cfgFile, "config", "", "config file")
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func runIndexer(cfgFile string) error {
	cfg, err := config.Load(cfgFile)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	log := observability.NewLogger(cfg.Observability.LogLevel)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	interval, err := time.ParseDuration(cfg.ToolsRegistry.PollInterval)
	if err != nil {
		interval = 60 * time.Second
	}

	// Optional Postgres — persists policy states across restarts.
	var db *store.DB
	if cfg.Postgres.DSN != "" {
		db, err = store.Open(ctx, cfg.Postgres.DSN)
		if err != nil {
			log.Warn("postgres unavailable — policy states will not be persisted", "error", err)
		} else {
			defer db.Close()
			log.Info("connected to postgres")
		}
	}

	// Choose source: git or local seed.
	var src tool.Source
	if cfg.ToolsRegistry.GitURL != "" {
		src = tool.NewGitSource(cfg.ToolsRegistry.GitURL, cfg.ToolsRegistry.Branch)
	} else {
		seedPath := cfg.ToolsRegistry.SeedPath
		if seedPath == "" {
			seedPath = "./tools-registry-seed"
		}
		src = tool.NewLocalSource(seedPath)
	}

	registry := tool.NewRegistry()

	log.Info("indexer starting", "interval", interval.String())
	if err := index(ctx, log, src, registry, db); err != nil {
		log.Warn("initial index failed", "error", err)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info("indexer stopping")
			return nil
		case <-ticker.C:
			if err := index(ctx, log, src, registry, db); err != nil {
				log.Warn("index refresh failed", "error", err)
			}
		}
	}
}

func index(ctx context.Context, log *slog.Logger, src tool.Source, reg *tool.Registry, db *store.DB) error {
	manifests, err := src.Load(ctx)
	if err != nil {
		return fmt.Errorf("load manifests: %w", err)
	}

	loaded, skipped := 0, 0
	for _, m := range manifests {
		if err := reg.Register(m); err != nil {
			log.Error("skipping tool — registration error", "tool_id", m.ID, "error", err)
			skipped++
			continue
		}

		// Restore persisted policy state if DB is available.
		if db != nil {
			if state, dbErr := db.GetToolPolicyState(ctx, m.ID); dbErr == nil {
				_ = reg.SetPolicyState(m.ID, state)
			} else {
				// First time seeing this tool — persist with default review state.
				_ = db.UpsertToolIndex(ctx, m, policy.StateReview)
			}
		}

		loaded++
	}

	log.Info("index refreshed", "loaded", loaded, "skipped", skipped, "total", reg.Len())
	return nil
}
