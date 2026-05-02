package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nithinkuma/opsmate/pkg/intent"
)

// IntentRow is the DB representation of a stored Intent.
type IntentRow struct {
	ID            string
	SchemaVersion string
	TicketID      string
	SourceSystem  string
	Verb          string
	TargetRepo    string
	TargetBranch  string
	Parameters    map[string]interface{}
	Policy        intent.Policy
	RawContext    intent.Context
	Status        string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// SaveIntent persists a new Intent to the database.
func (db *DB) SaveIntent(ctx context.Context, i *intent.Intent) error {
	params, err := json.Marshal(i.Action.Parameters)
	if err != nil {
		return fmt.Errorf("store: marshal params: %w", err)
	}
	pol, err := json.Marshal(i.Policy)
	if err != nil {
		return fmt.Errorf("store: marshal policy: %w", err)
	}
	raw, err := json.Marshal(i.Context)
	if err != nil {
		return fmt.Errorf("store: marshal context: %w", err)
	}

	_, err = db.Pool.Exec(ctx, `
		INSERT INTO intents
		  (id, schema_version, ticket_id, source_system, verb,
		   target_repo, target_branch, parameters, policy, raw_context, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'pending')
		ON CONFLICT (id) DO NOTHING`,
		i.IntentID, i.SchemaVersion, i.Source.TicketID, i.Source.System,
		i.Action.Verb, i.Action.Target.Repo, i.Action.Target.Branch,
		params, pol, raw,
	)
	return err
}

// UpdateIntentStatus changes the status field of a stored intent.
func (db *DB) UpdateIntentStatus(ctx context.Context, intentID, status string) error {
	_, err := db.Pool.Exec(ctx,
		`UPDATE intents SET status=$1, updated_at=NOW() WHERE id=$2`,
		status, intentID,
	)
	return err
}

// GetIntent fetches one intent by ID.
func (db *DB) GetIntent(ctx context.Context, intentID string) (*IntentRow, error) {
	row := db.Pool.QueryRow(ctx, `
		SELECT id, schema_version, ticket_id, source_system, verb,
		       target_repo, target_branch, parameters, policy, raw_context,
		       status, created_at, updated_at
		FROM intents WHERE id=$1`, intentID)

	var r IntentRow
	var params, pol, rawCtx []byte
	err := row.Scan(
		&r.ID, &r.SchemaVersion, &r.TicketID, &r.SourceSystem, &r.Verb,
		&r.TargetRepo, &r.TargetBranch, &params, &pol, &rawCtx,
		&r.Status, &r.CreatedAt, &r.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("store: get intent %s: %w", intentID, err)
	}
	_ = json.Unmarshal(params, &r.Parameters)
	_ = json.Unmarshal(pol, &r.Policy)
	_ = json.Unmarshal(rawCtx, &r.RawContext)
	return &r, nil
}
