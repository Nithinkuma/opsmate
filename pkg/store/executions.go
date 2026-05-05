package store

import (
	"context"
	"fmt"
	"time"
)

// ExecutionStatus values.
const (
	ExecPending  = "pending"
	ExecRunning  = "running"
	ExecSuccess  = "success"
	ExecFailed   = "failed"
	ExecTimeout  = "timeout"
)

// Execution tracks one tool run from start to PR creation.
type Execution struct {
	ID           string
	IntentID     string
	ToolID       string
	ManifestHash string
	Status       string
	SandboxKind  string
	PodName      string
	ExitCode     *int
	DiffOutput   string
	PRURL        string
	ErrorMessage string
	StartedAt    *time.Time
	FinishedAt   *time.Time
	CreatedAt    time.Time
}

// CreateExecution inserts a new execution row.
func (db *DB) CreateExecution(ctx context.Context, e *Execution) error {
	_, err := db.Pool.Exec(ctx, `
		INSERT INTO executions
		  (id, intent_id, tool_id, manifest_hash, status, sandbox_kind)
		VALUES ($1,$2,$3,$4,$5,$6)`,
		e.ID, e.IntentID, e.ToolID, e.ManifestHash, e.Status, e.SandboxKind,
	)
	return err
}

// UpdateExecution updates mutable fields on an execution row.
func (db *DB) UpdateExecution(ctx context.Context, e *Execution) error {
	_, err := db.Pool.Exec(ctx, `
		UPDATE executions SET
		  status=$1, pod_name=$2, exit_code=$3,
		  diff_output=$4, pr_url=$5, error_message=$6,
		  started_at=$7, finished_at=$8
		WHERE id=$9`,
		e.Status, e.PodName, e.ExitCode,
		e.DiffOutput, e.PRURL, e.ErrorMessage,
		e.StartedAt, e.FinishedAt, e.ID,
	)
	return err
}

// GetExecution fetches a single execution row by ID.
func (db *DB) GetExecution(ctx context.Context, id string) (*Execution, error) {
	row := db.Pool.QueryRow(ctx, `
		SELECT id, intent_id, tool_id, manifest_hash, status, sandbox_kind,
		       pod_name, exit_code, diff_output, pr_url, error_message,
		       started_at, finished_at, created_at
		FROM executions WHERE id=$1`, id)
	e := &Execution{}
	err := row.Scan(
		&e.ID, &e.IntentID, &e.ToolID, &e.ManifestHash, &e.Status, &e.SandboxKind,
		&e.PodName, &e.ExitCode, &e.DiffOutput, &e.PRURL, &e.ErrorMessage,
		&e.StartedAt, &e.FinishedAt, &e.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("store: execution %q not found: %w", id, err)
	}
	return e, nil
}

// WriteAuditLog appends one append-only audit entry to tool_runs.
func (db *DB) WriteAuditLog(ctx context.Context,
	executionID, toolID, manifestHash, actor, result, targetRepo, prURL, intentID string,
) error {
	id := fmt.Sprintf("tr_%d", time.Now().UnixNano())
	_, err := db.Pool.Exec(ctx, `
		INSERT INTO tool_runs
		  (id, execution_id, tool_id, manifest_hash, actor, result,
		   target_repo, pr_url, intent_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		id, executionID, toolID, manifestHash, actor, result, targetRepo, prURL, intentID,
	)
	return err
}
