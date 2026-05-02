package store

import (
	"context"
	"fmt"

	"github.com/oklog/ulid/v2"
)

// PRSummary is one indexed pull-request record used for BM25 search.
type PRSummary struct {
	ID      string
	Repo    string
	Verb    string
	PRURL   string
	Title   string
	Summary string
}

// SavePRSummary inserts a PR summary for future BM25 search.
func (db *DB) SavePRSummary(ctx context.Context, s *PRSummary) error {
	if s.ID == "" {
		s.ID = ulid.Make().String()
	}
	_, err := db.Pool.Exec(ctx, `
		INSERT INTO pr_summaries (id, repo, verb, pr_url, title, summary)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (id) DO NOTHING`,
		s.ID, s.Repo, s.Verb, s.PRURL, s.Title, s.Summary,
	)
	return err
}

// SearchPRSummaries performs a Postgres full-text (BM25-style) search over
// the summary corpus and returns up to limit matching records.
func (db *DB) SearchPRSummaries(ctx context.Context, query string, limit int) ([]*PRSummary, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := db.Pool.Query(ctx, `
		SELECT id, repo, verb, pr_url, title, summary
		FROM pr_summaries
		WHERE tsv @@ plainto_tsquery('english', $1)
		ORDER BY ts_rank_cd(tsv, plainto_tsquery('english', $1)) DESC
		LIMIT $2`,
		query, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("store: search pr summaries: %w", err)
	}
	defer rows.Close()

	var results []*PRSummary
	for rows.Next() {
		var s PRSummary
		if err := rows.Scan(&s.ID, &s.Repo, &s.Verb, &s.PRURL, &s.Title, &s.Summary); err != nil {
			return nil, fmt.Errorf("store: scan pr summary: %w", err)
		}
		results = append(results, &s)
	}
	return results, rows.Err()
}
