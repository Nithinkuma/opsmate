package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/nithinkuma/opsmate/pkg/intent"
	"github.com/nithinkuma/opsmate/pkg/mcp"
	"github.com/nithinkuma/opsmate/pkg/observability"
	"github.com/nithinkuma/opsmate/pkg/store"
	"github.com/nithinkuma/opsmate/pkg/tool"
)

// PRRaiser creates a branch, applies the diff, opens a PR, and comments on Jira.
type PRRaiser struct {
	bitbucket *mcp.BitbucketClient
	atlassian *mcp.AtlassianClient
	db        *store.DB
	log       *slog.Logger
}

// NewPRRaiser constructs a PRRaiser with the given MCP clients.
func NewPRRaiser(
	bitbucket *mcp.BitbucketClient,
	atlassian *mcp.AtlassianClient,
	db *store.DB,
	log *slog.Logger,
) *PRRaiser {
	return &PRRaiser{
		bitbucket: bitbucket,
		atlassian: atlassian,
		db:        db,
		log:       log,
	}
}

// RaiseResult holds the URLs produced by a successful PR creation.
type RaiseResult struct {
	PRURL  string
	Branch string
}

// Raise applies the diff to a new branch and opens a pull request.
func (r *PRRaiser) Raise(
	ctx context.Context,
	i *intent.Intent,
	entry *tool.Entry,
	exec *ExecResult,
) (*RaiseResult, error) {
	ctx, span := observability.Start(ctx, "pr_raiser.raise")
	defer span.End()

	branch := fmt.Sprintf("automation/%s/%s", i.Action.Verb, i.Source.TicketID)

	intentJSON, _ := json.MarshalIndent(i, "", "  ")
	prBody := fmt.Sprintf(`## Automated PR

**Jira ticket**: [%s](%s)
**Verb**: %s
**Tool ID**: %s
**Manifest hash**: %s

<details><summary>Intent JSON</summary>

`+"`"+`"`+"`"+`json
%s
`+"`"+`"`+"`"+`
</details>

This PR was generated automatically by OpsMate. Review the diff carefully before merging.`,
		i.Source.TicketID, i.Source.URL,
		i.Action.Verb,
		entry.Manifest.ID,
		entry.Manifest.Hash.Script,
		string(intentJSON),
	)

	pr, err := r.bitbucket.CreatePR(ctx, mcp.CreatePRRequest{
		Repo:         i.Action.Target.Repo,
		Title:        fmt.Sprintf("[%s] %s", i.Source.TicketID, i.Action.Verb),
		Description:  prBody,
		SourceBranch: branch,
		TargetBranch: i.Action.Target.Branch,
	})
	if err != nil {
		return nil, fmt.Errorf("pr_raiser: create PR: %w", err)
	}

	// Comment PR link back to Jira.
	jiraComment := fmt.Sprintf("OpsMate has created a pull request for this ticket:\n%s", pr.URL)
	if err := r.atlassian.AddComment(ctx, i.Source.TicketID, jiraComment); err != nil {
		r.log.WarnContext(ctx, "failed to comment on Jira", "error", err)
	}

	// Persist audit log entry.
	if r.db != nil {
		_ = r.db.WriteAuditLog(ctx,
			exec.ExecutionID, entry.Manifest.ID, entry.Manifest.Hash.Script,
			"opsmate-agent", "success", i.Action.Target.Repo, pr.URL, i.IntentID,
		)
	}

	r.log.InfoContext(ctx, "pr raised",
		"pr_url", pr.URL,
		"branch", branch,
		"tool_id", entry.Manifest.ID,
	)

	return &RaiseResult{PRURL: pr.URL, Branch: branch}, nil
}
