package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nithinkuma/opsmate/pkg/policy"
	"github.com/nithinkuma/opsmate/pkg/tool"
)

// UpsertToolIndex inserts or updates a manifest entry in the tool_index table.
func (db *DB) UpsertToolIndex(ctx context.Context, m *tool.Manifest, state policy.State) error {
	manifestJSON, _ := json.Marshal(m)
	_, err := db.Pool.Exec(ctx, `
		INSERT INTO tool_index
		  (tool_id, verb, repo_pattern, manifest_hash, script_hash, manifest_yaml, policy_state)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (tool_id) DO UPDATE SET
		  verb          = EXCLUDED.verb,
		  repo_pattern  = EXCLUDED.repo_pattern,
		  manifest_hash = EXCLUDED.manifest_hash,
		  script_hash   = EXCLUDED.script_hash,
		  manifest_yaml = EXCLUDED.manifest_yaml,
		  updated_at    = NOW()`,
		m.ID, m.Verb, m.Repo.Pattern,
		m.Hash.Manifest, m.Hash.Script, string(manifestJSON),
		string(state),
	)
	return err
}

// SetToolPolicyState updates the policy_state for a tool in the index.
func (db *DB) SetToolPolicyState(ctx context.Context, toolID string, state policy.State) error {
	tag, err := db.Pool.Exec(ctx,
		`UPDATE tool_index SET policy_state=$1, updated_at=$2 WHERE tool_id=$3`,
		string(state), time.Now(), toolID,
	)
	if err != nil {
		return fmt.Errorf("store: set policy state %s: %w", toolID, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("store: tool %q not found in index", toolID)
	}
	return nil
}

// GetToolPolicyState returns the persisted policy_state for a tool.
func (db *DB) GetToolPolicyState(ctx context.Context, toolID string) (policy.State, error) {
	var state string
	err := db.Pool.QueryRow(ctx,
		`SELECT policy_state FROM tool_index WHERE tool_id=$1`, toolID,
	).Scan(&state)
	if err != nil {
		return "", fmt.Errorf("store: get policy state %s: %w", toolID, err)
	}
	return policy.State(state), nil
}
