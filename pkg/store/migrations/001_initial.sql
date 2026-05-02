-- 001_initial.sql
-- +goose Up
CREATE TABLE IF NOT EXISTS intents (
    id              TEXT PRIMARY KEY,
    schema_version  TEXT NOT NULL DEFAULT '1',
    ticket_id       TEXT NOT NULL,
    source_system   TEXT NOT NULL DEFAULT 'jira',
    verb            TEXT NOT NULL,
    target_repo     TEXT NOT NULL,
    target_branch   TEXT NOT NULL,
    parameters      JSONB NOT NULL DEFAULT '{}',
    policy          JSONB NOT NULL DEFAULT '{}',
    raw_context     JSONB NOT NULL DEFAULT '{}',
    status          TEXT NOT NULL DEFAULT 'pending',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS executions (
    id              TEXT PRIMARY KEY,
    intent_id       TEXT NOT NULL REFERENCES intents(id),
    tool_id         TEXT NOT NULL,
    manifest_hash   TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending',
    sandbox_kind    TEXT NOT NULL,
    pod_name        TEXT,
    exit_code       INT,
    diff_output     TEXT,
    pr_url          TEXT,
    error_message   TEXT,
    started_at      TIMESTAMPTZ,
    finished_at     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS tool_runs (
    id              TEXT PRIMARY KEY,
    execution_id    TEXT NOT NULL REFERENCES executions(id),
    tool_id         TEXT NOT NULL,
    manifest_hash   TEXT NOT NULL,
    actor           TEXT NOT NULL,
    result          TEXT NOT NULL,
    target_repo     TEXT NOT NULL,
    pr_url          TEXT,
    intent_id       TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS tool_runs_tool_id_idx ON tool_runs(tool_id);
CREATE INDEX IF NOT EXISTS tool_runs_created_at_idx ON tool_runs(created_at);

CREATE TABLE IF NOT EXISTS llm_traces (
    id              TEXT PRIMARY KEY,
    intent_id       TEXT,
    operation       TEXT NOT NULL,
    provider        TEXT NOT NULL,
    model           TEXT NOT NULL,
    input_tokens    INT NOT NULL DEFAULT 0,
    output_tokens   INT NOT NULL DEFAULT 0,
    cost_usd        NUMERIC(12,8) NOT NULL DEFAULT 0,
    latency_ms      INT NOT NULL DEFAULT 0,
    error           TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS llm_traces_intent_id_idx ON llm_traces(intent_id);

CREATE TABLE IF NOT EXISTS tool_index (
    tool_id         TEXT PRIMARY KEY,
    verb            TEXT NOT NULL,
    repo_pattern    TEXT NOT NULL,
    manifest_hash   TEXT NOT NULL,
    script_hash     TEXT NOT NULL,
    manifest_yaml   TEXT NOT NULL,
    approved_at     TIMESTAMPTZ,
    approval_pr     TEXT,
    policy_state    TEXT NOT NULL DEFAULT 'review',
    invocation_count INT NOT NULL DEFAULT 0,
    last_success_at TIMESTAMPTZ,
    last_failure_at TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS tool_index_verb_idx ON tool_index(verb);

CREATE TABLE IF NOT EXISTS evals (
    id              TEXT PRIMARY KEY,
    case_name       TEXT NOT NULL,
    intent_id       TEXT,
    tool_id         TEXT,
    expected_diff   TEXT NOT NULL,
    actual_diff     TEXT,
    passed          BOOLEAN,
    run_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose Down
DROP TABLE IF EXISTS evals;
DROP TABLE IF EXISTS tool_index;
DROP TABLE IF EXISTS llm_traces;
DROP TABLE IF EXISTS tool_runs;
DROP TABLE IF EXISTS executions;
DROP TABLE IF EXISTS intents;
