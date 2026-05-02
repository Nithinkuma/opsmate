package store

import (
	"context"
	"fmt"
	"time"

	"github.com/nithinkuma/opsmate/pkg/llm"
)

// LLMTrace records a single LLM call for cost accounting and debugging.
type LLMTrace struct {
	ID         string
	IntentID   string
	Operation  string
	Provider   string
	Model      string
	InputTokens  int
	OutputTokens int
	CostUSD    float64
	LatencyMS  int
	Error      string
	CreatedAt  time.Time
}

// SaveLLMTrace persists an LLM call trace.
func (db *DB) SaveLLMTrace(ctx context.Context, t *LLMTrace) error {
	if t.ID == "" {
		t.ID = fmt.Sprintf("lt_%d", time.Now().UnixNano())
	}
	_, err := db.Pool.Exec(ctx, `
		INSERT INTO llm_traces
		  (id, intent_id, operation, provider, model,
		   input_tokens, output_tokens, cost_usd, latency_ms, error)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		t.ID, t.IntentID, t.Operation, t.Provider, t.Model,
		t.InputTokens, t.OutputTokens, t.CostUSD, t.LatencyMS, t.Error,
	)
	return err
}

// TracingClient wraps an llm.Client and records every call to Postgres.
type TracingClient struct {
	inner     llm.Client
	db        *DB
	intentID  string
	operation string
}

// NewTracingClient wraps client so every Complete() is traced to Postgres.
func NewTracingClient(inner llm.Client, db *DB, intentID, operation string) *TracingClient {
	return &TracingClient{inner: inner, db: db, intentID: intentID, operation: operation}
}

func (t *TracingClient) ProviderName() string { return t.inner.ProviderName() }

func (t *TracingClient) Complete(ctx context.Context, req llm.Request) (llm.Response, error) {
	start := time.Now()
	resp, err := t.inner.Complete(ctx, req)
	latency := int(time.Since(start).Milliseconds())

	trace := &LLMTrace{
		IntentID:     t.intentID,
		Operation:    t.operation,
		Provider:     t.inner.ProviderName(),
		Model:        req.Model,
		InputTokens:  resp.Usage.InputTokens,
		OutputTokens: resp.Usage.OutputTokens,
		CostUSD:      resp.Usage.CostUSD,
		LatencyMS:    latency,
	}
	if err != nil {
		trace.Error = err.Error()
	}
	// Best-effort — don't let a trace failure abort the LLM call result.
	_ = t.db.SaveLLMTrace(ctx, trace)
	return resp, err
}
