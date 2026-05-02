-- 002_pr_summaries.sql
-- +goose Up
CREATE TABLE IF NOT EXISTS pr_summaries (
    id              TEXT PRIMARY KEY,
    repo            TEXT NOT NULL,
    verb            TEXT NOT NULL,
    pr_url          TEXT NOT NULL,
    title           TEXT NOT NULL,
    summary         TEXT NOT NULL,
    tsv             TSVECTOR GENERATED ALWAYS AS (
                        to_tsvector('english', title || ' ' || summary)
                    ) STORED,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS pr_summaries_tsv_idx ON pr_summaries USING GIN(tsv);
CREATE INDEX IF NOT EXISTS pr_summaries_repo_verb_idx ON pr_summaries(repo, verb);

-- +goose Down
DROP TABLE IF EXISTS pr_summaries;
