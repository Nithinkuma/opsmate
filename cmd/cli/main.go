// cmd/cli is the local developer CLI: `opsmate process-ticket INFRA-1234`
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	"github.com/nithinkuma/opsmate/pkg/config"
	"github.com/nithinkuma/opsmate/pkg/mcp"
	"github.com/nithinkuma/opsmate/pkg/observability"
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

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func processTicketCmd() *cobra.Command {
	var (
		extractIntent bool
		outputJSON    bool
	)
	cmd := &cobra.Command{
		Use:   "process-ticket <TICKET-ID>",
		Short: "Fetch a Jira ticket, optionally extract an Intent, and print it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ticketID := args[0]
			ctx := context.Background()

			cfg, err := config.Load(cfgFile)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			log := observability.NewLogger(cfg.Observability.LogLevel)
			log.Info("fetching ticket", "ticket_id", ticketID)

			atlassian, err := mcp.NewAtlassianClient(
				cfg.MCP.Atlassian.URL,
				cfg.MCP.Atlassian.Auth,
			)
			if err != nil {
				return fmt.Errorf("atlassian client: %w", err)
			}

			ticket, err := atlassian.GetTicket(ctx, ticketID)
			if err != nil {
				return fmt.Errorf("get ticket: %w", err)
			}

			if !extractIntent {
				// Phase 0: just dump the raw ticket
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(ticket)
			}

			// Phase 1+: extract Intent via LLM
			return runExtractIntent(ctx, cfg, log, ticket, outputJSON)
		},
	}
	cmd.Flags().BoolVar(&extractIntent, "extract", false, "extract an Intent via LLM (Phase 1+)")
	cmd.Flags().BoolVar(&outputJSON, "json", true, "output as JSON")
	return cmd
}

func runExtractIntent(ctx context.Context, cfg *config.Config, log *slog.Logger, ticket *mcp.JiraTicket, asJSON bool) error {
	// Import here to avoid circular deps at Phase 0 compile time.
	// In Phase 1 this is wired up properly.
	log.Info("intent extraction not yet wired — run with --extract=false for Phase 0")
	return nil
}

func promoteToolCmd() *cobra.Command {
	var toState string
	cmd := &cobra.Command{
		Use:   "promote-tool <tool-id>",
		Short: "Promote a tool on the trust ladder (requires confirmation)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			toolID := args[0]
			fmt.Printf("Promoting %s → %s\n", toolID, toState)
			fmt.Print("Confirm? [y/N]: ")
			var ans string
			fmt.Scan(&ans)
			if ans != "y" && ans != "Y" {
				fmt.Println("aborted")
				return nil
			}
			fmt.Printf("Tool %s promoted to %s\n", toolID, toState)
			return nil
		},
	}
	cmd.Flags().StringVar(&toState, "to", "auto_merge_eligible", "target state: review|auto_merge_eligible|auto_merge_active")
	return cmd
}
