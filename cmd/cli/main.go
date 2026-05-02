// cmd/cli is the local developer CLI: `opsmate process-ticket INFRA-1234`
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

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

var (
	cfgFile string
	rootCmd = &cobra.Command{
		Use:   "opsmate",
		Short: "Jira-to-PR automation agent CLI",
	}
)

func main() {
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default: ./config.yaml)")
	rootCmd.AddCommand(processTicketCmd())
	rootCmd.AddCommand(promoteToolCmd())
	rootCmd.AddCommand(evalCmd())

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// ── process-ticket ────────────────────────────────────────────────────────────

func processTicketCmd() *cobra.Command {
	var (
		runPipelineFlag bool
		outputJSON      bool
	)
	cmd := &cobra.Command{
		Use:   "process-ticket <TICKET-ID>",
		Short: "Fetch a Jira ticket; with --run, execute the full pipeline",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ticketID := args[0]
			ctx := context.Background()

			cfg, err := config.Load(cfgFile)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			log := observability.NewLogger(cfg.Observability.LogLevel)

			if !runPipelineFlag {
				// Phase 0: dump raw ticket
				atlassian, err := mcp.NewAtlassianClient(cfg.MCP.Atlassian.URL, cfg.MCP.Atlassian.Auth)
				if err != nil {
					return fmt.Errorf("atlassian client: %w", err)
				}
				ticket, err := atlassian.GetTicket(ctx, ticketID)
				if err != nil {
					return fmt.Errorf("get ticket: %w", err)
				}
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(ticket)
			}

			return runFullPipeline(ctx, cfg, log, ticketID, outputJSON)
		},
	}
	cmd.Flags().BoolVar(&runPipelineFlag, "run", false, "run the full extraction+execution pipeline")
	cmd.Flags().BoolVar(&outputJSON, "json", true, "print result as JSON")
	return cmd
}

func runFullPipeline(ctx context.Context, cfg *config.Config, log *slog.Logger, ticketID string, asJSON bool) error {
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
	if manifests, err := src.Load(ctx); err != nil {
		log.Warn("tool registry load warning", "error", err)
	} else {
		for _, m := range manifests {
			if err := toolReg.Register(m); err != nil {
				log.Warn("skipping tool", "id", m.ID, "error", err)
			}
		}
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
		GeneratorCfg: generatorCfg(cfg),
		Log:          log,
	})

	result, err := pipeline.Run(ctx, ticketID)
	if err != nil {
		return err
	}

	if result.ClarificationNeeded != "" {
		fmt.Fprintln(os.Stderr, "Clarification needed:", result.ClarificationNeeded)
		return nil
	}

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}

	if result.ToolGenerated {
		fmt.Printf("New tool generated for verb %q — awaiting human review\n", result.Intent.Action.Verb)
		return nil
	}
	fmt.Println("PR:", result.PRURL)
	fmt.Println("Branch:", result.Branch)
	return nil
}

// ── promote-tool ─────────────────────────────────────────────────────────────

func promoteToolCmd() *cobra.Command {
	var toState string
	cmd := &cobra.Command{
		Use:   "promote-tool <tool-id>",
		Short: "Promote a tool on the trust ladder (persisted to Postgres)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			toolID := args[0]
			ctx := context.Background()

			state := policy.State(toState)
			switch state {
			case policy.StateReview, policy.StateAutoMergeEligible, policy.StateAutoMergeActive:
			default:
				return fmt.Errorf("invalid state %q; must be review|auto_merge_eligible|auto_merge_active", toState)
			}

			fmt.Printf("Promoting %s → %s\n", toolID, state)
			fmt.Print("Confirm? [y/N]: ")
			var ans string
			fmt.Scan(&ans)
			if ans != "y" && ans != "Y" {
				fmt.Println("aborted")
				return nil
			}

			cfg, err := config.Load(cfgFile)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			if cfg.Postgres.DSN == "" {
				fmt.Println("no postgres DSN configured — state change not persisted")
				return nil
			}

			db, err := store.Open(ctx, cfg.Postgres.DSN)
			if err != nil {
				return fmt.Errorf("connect postgres: %w", err)
			}
			defer db.Close()

			if err := db.SetToolPolicyState(ctx, toolID, state); err != nil {
				return fmt.Errorf("set policy state: %w", err)
			}
			fmt.Printf("Tool %s promoted to %s\n", toolID, state)
			return nil
		},
	}
	cmd.Flags().StringVar(&toState, "to", "auto_merge_eligible",
		"target state: review|auto_merge_eligible|auto_merge_active")
	return cmd
}

// ── eval ─────────────────────────────────────────────────────────────────────

func evalCmd() *cobra.Command {
	var (
		goldenDir string
		verbose   bool
	)
	cmd := &cobra.Command{
		Use:   "eval",
		Short: "Run golden-test eval cases against the local sandbox",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			cfg, err := config.Load(cfgFile)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			log := observability.NewLogger(cfg.Observability.LogLevel)

			// Load golden cases
			cases, err := eval.LoadCases(goldenDir)
			if err != nil {
				return fmt.Errorf("load golden cases: %w", err)
			}
			if len(cases) == 0 {
				fmt.Println("no golden cases found in", goldenDir)
				return nil
			}
			fmt.Printf("Running %d golden case(s) from %s\n\n", len(cases), goldenDir)

			// Tool registry (local seed only — no MCP needed for eval)
			toolReg := tool.NewRegistry()
			src := toolSource(cfg)
			if manifests, err := src.Load(ctx); err != nil {
				log.Warn("tool registry load warning", "error", err)
			} else {
				for _, m := range manifests {
					if err := toolReg.Register(m); err != nil {
						log.Warn("skipping tool", "id", m.ID, "error", err)
					}
				}
			}

			runner := sandbox.NewLocalRunner(log)
			evalRunner := eval.NewRunner(toolReg, runner)
			results := evalRunner.RunAll(ctx, cases)

			if verbose {
				for _, r := range results {
					if !r.Passed || r.Error != "" {
						fmt.Printf("--- %s ---\nGOT:\n%s\n\n", r.Name, r.Got)
					}
				}
			}

			fmt.Println(eval.Summary(results))

			// Exit 1 if any failure
			for _, r := range results {
				if !r.Passed || r.Error != "" {
					os.Exit(1)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&goldenDir, "golden-dir", "eval/golden", "directory containing golden JSON test cases")
	cmd.Flags().BoolVar(&verbose, "verbose", false, "print diff output for failing cases")
	return cmd
}

// ── helpers ───────────────────────────────────────────────────────────────────

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

func generatorCfg(cfg *config.Config) agent.GeneratorConfig {
	maxSteps := cfg.Agent.GeneratorMaxSteps
	if maxSteps == 0 {
		maxSteps = 15
	}
	maxParallel := cfg.Agent.GeneratorMaxParallelTools
	if maxParallel == 0 {
		maxParallel = 4
	}
	return agent.GeneratorConfig{
		MaxSteps:         maxSteps,
		MaxParallelTools: maxParallel,
		Temperature:      cfg.Agent.GeneratorTemperature,
		Model:            cfg.LLM.Primary.Model,
	}
}
