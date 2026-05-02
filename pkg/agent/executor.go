package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/nithinkuma/opsmate/pkg/intent"
	"github.com/nithinkuma/opsmate/pkg/observability"
	"github.com/nithinkuma/opsmate/pkg/sandbox"
	"github.com/nithinkuma/opsmate/pkg/store"
	"github.com/nithinkuma/opsmate/pkg/tool"
	"github.com/oklog/ulid/v2"
)

// Executor verifies manifest hashes and runs a tool in the sandbox.
type Executor struct {
	runner      sandbox.Runner
	db          *store.DB
	sandboxKind string
	log         *slog.Logger
}

// NewExecutor creates an Executor using the given sandbox runner.
func NewExecutor(runner sandbox.Runner, db *store.DB, sandboxKind string, log *slog.Logger) *Executor {
	return &Executor{runner: runner, db: db, sandboxKind: sandboxKind, log: log}
}

// ExecResult holds the diff output and execution metadata.
type ExecResult struct {
	ExecutionID string
	Diff        string
	PodName     string
	Duration    time.Duration
}

// Execute runs the tool for the given intent and returns the unified diff.
func (e *Executor) Execute(ctx context.Context, i *intent.Intent, entry *tool.Entry) (*ExecResult, error) {
	ctx, span := observability.Start(ctx, "executor.execute")
	defer span.End()

	// ── Hash verification (hard failure per spec §4) ──────────────────────────
	if err := entry.Manifest.VerifyHashes(); err != nil {
		return nil, fmt.Errorf("executor: hash mismatch — refusing to run: %w", err)
	}

	// ── Materialise params ───────────────────────────────────────────────────
	paramsJSON, err := json.Marshal(i.Action.Parameters)
	if err != nil {
		return nil, fmt.Errorf("executor: marshal params: %w", err)
	}

	execID := ulid.Make().String()
	exec := &store.Execution{
		ID:           execID,
		IntentID:     i.IntentID,
		ToolID:       entry.Manifest.ID,
		ManifestHash: entry.Manifest.Hash.Script,
		Status:       store.ExecRunning,
		SandboxKind:  e.sandboxKind,
	}
	if e.db != nil {
		_ = e.db.CreateExecution(ctx, exec)
	}

	spec := sandbox.Spec{
		ToolID:         entry.Manifest.ID,
		Language:       sandbox.Language(entry.Manifest.Runtime.Language),
		ScriptContent:  entry.Manifest.ScriptContent,
		ParamsJSON:     string(paramsJSON),
		RepoURL:        repoURL(i.Action.Target.Repo),
		RepoBranch:     i.Action.Target.Branch,
		Dependencies:   entry.Manifest.Runtime.Dependencies,
		TimeoutSeconds: 300,
	}

	start := time.Now()
	result, err := e.runner.Run(ctx, spec)
	duration := time.Since(start)

	if err != nil || result.ExitCode != 0 {
		status := store.ExecFailed
		errMsg := fmt.Sprintf("exit code %d", result.ExitCode)
		if err != nil {
			errMsg = err.Error()
		}
		exec.Status = status
		exec.ErrorMessage = errMsg
		exec.FinishedAt = &result.FinishedAt
		if e.db != nil {
			_ = e.db.UpdateExecution(ctx, exec)
		}
		return nil, fmt.Errorf("executor: tool failed: %s", errMsg)
	}

	exec.Status = store.ExecSuccess
	exec.DiffOutput = result.Stdout
	exec.PodName = result.PodName
	exec.FinishedAt = &result.FinishedAt
	if e.db != nil {
		_ = e.db.UpdateExecution(ctx, exec)
	}

	e.log.InfoContext(ctx, "execution complete",
		"tool_id", entry.Manifest.ID,
		"duration_ms", duration.Milliseconds(),
		"diff_bytes", len(result.Stdout),
	)

	return &ExecResult{
		ExecutionID: execID,
		Diff:        result.Stdout,
		PodName:     result.PodName,
		Duration:    duration,
	}, nil
}

// repoURL converts "org/repo" to a git clone URL.
// In production this would use the configured Bitbucket base URL.
func repoURL(repo string) string {
	return "https://bitbucket.org/" + repo + ".git"
}
