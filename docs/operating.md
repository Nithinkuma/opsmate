# Operating Opsmate

## Reading OTEL Traces

All components export traces to the configured OTLP endpoint
(`OTEL_EXPORTER_OTLP_ENDPOINT`).  The most useful spans are:

| Span name | Component | Key attributes |
|---|---|---|
| `intent_agent.process` | Intent Agent | `ticket_id`, `verb`, `repo`, `latency_ms` |
| `resolver.lookup` | Resolver | `verb`, `repo`, `cache_hit` |
| `executor.run_job` | Executor | `job_name`, `exit_code`, `diff_bytes` |
| `pr_raiser.open_pr` | PR Raiser | `pr_url`, `repo` |
| `generator.generate` | Generator | `verb`, `repo`, `model`, `input_tokens`, `output_tokens` |
| `indexer.ingest` | Indexer | `tool_id`, `hash_ok` |

**LLM call details** are stored separately in the `llm_traces` Postgres table
(see `pkg/store/llm_traces.go`).  Columns of interest:

- `id` — ULID
- `intent_id` — links back to the intent that triggered this call
- `model` — model name used
- `input_tokens`, `output_tokens` — for cost tracking
- `prompt_snapshot` — full prompt (JSONB); useful for debugging hallucinations
- `created_at`

Query example — cost per verb over the last 7 days:

```sql
SELECT
  i.verb,
  COUNT(*)                                    AS calls,
  SUM(l.input_tokens + l.output_tokens)       AS total_tokens
FROM llm_traces l
JOIN intents i ON i.id = l.intent_id
WHERE l.created_at > NOW() - INTERVAL '7 days'
GROUP BY i.verb
ORDER BY total_tokens DESC;
```

---

## Alert Reference

### `generator_escalation`

**Meaning:** The Resolver could not find a tool for the `(verb, repo)` pair and
escalated to the Generator.

**Action:** Monitor the tools-registry PR opened by the Generator.  If the
generated script looks correct, approve and merge.  The Indexer will pick it up
automatically.  If the Generator failed or produced a bad script, see
[Handling a Failed Generator Run](#handling-a-failed-generator-run).

---

### `sandbox_timeout`

**Meaning:** A tool Job in `agent-sandbox` exceeded its deadline (default 120 s).

**Action:**
1. Find the Job: `kubectl get jobs -n agent-sandbox -l intent_id=<id>`
2. Inspect logs: `kubectl logs -n agent-sandbox job/<name>`
3. Common causes: slow filesystem scan in the repo clone, infinite loop in a
   generated script.
4. If the script is at fault, revise it in tools-registry and re-trigger via
   `opsmate retry-intent <intent_id>`.

---

### `hash_mismatch`

**Meaning:** The SHA-256 hash of a tool's `manifest.yaml` or `script.py` on
disk does not match the value stored in the Registry database.

**Action:** This is a security-critical alert.  Do not execute the tool.
1. Identify the tool: check the `tool_id` attribute in the alert.
2. Compare `sha256sum` of the file against the DB record.
3. If the mismatch is accidental (e.g. a hot-fix applied without going through
   the Indexer), re-run `opsmate promote-tool <tool_id>` to recompute and store
   the correct hashes.
4. If the mismatch is unexplained, treat it as a potential supply-chain
   compromise and escalate to the security team.

---

### `mcp_unavailable`

**Meaning:** The Jira or Bitbucket MCP server returned an error or is
unreachable.

**Action:**
1. Check the MCP server health endpoint.
2. Verify the relevant token has not expired (`JIRA_MCP_TOKEN`,
   `BITBUCKET_MCP_TOKEN` in the agent's secret).
3. Intents that failed to fetch will be retried automatically with exponential
   back-off up to 5 times.  After that they move to `FAILED` state.

---

## Promoting a Tool

The `opsmate promote-tool` command hashes a tool directory and upserts the
record into the Registry database.  Use it after a manual edit to a seed tool
or after the Indexer fails.

```bash
opsmate promote-tool \
  --tool-dir tools-registry-seed/tools/org__payments-service/update_dependency
```

This will:
1. Compute `sha256` of `manifest.yaml` and `script.py`.
2. Validate the manifest schema.
3. Upsert the tool record in the `tools` table.
4. Print the new `tool_id` and hashes.

Run with `--dry-run` to preview changes without writing to the DB.

---

## Handling a Failed Generator Run

1. Find the failing Generator span in your tracing backend; note the
   `intent_id`.
2. Check `llm_traces` for that `intent_id` to see the raw prompt and response:
   ```sql
   SELECT prompt_snapshot, response_snapshot
   FROM llm_traces
   WHERE intent_id = '<id>'
   ORDER BY created_at DESC
   LIMIT 1;
   ```
3. If the LLM produced a syntactically broken script, close the draft PR and
   re-trigger generation:
   ```bash
   opsmate retry-intent <intent_id> --force-generate
   ```
4. If generation keeps failing for a specific `(verb, repo)`, write the tool by
   hand following [Adding a Verb](adding-a-verb.md) and promote it.

---

## Reading the Audit Log

Every state transition for an intent is appended to the `executions` table
(`pkg/store/executions.go`).  Key columns:

| Column | Meaning |
|---|---|
| `intent_id` | Links to `intents.id` |
| `tool_id` | Which tool was used |
| `state` | `PENDING`, `RUNNING`, `SUCCEEDED`, `FAILED`, `WAITING_TOOL` |
| `exit_code` | Tool Job exit code |
| `diff_preview` | First 4 KiB of the unified diff |
| `pr_url` | PR opened (if state = SUCCEEDED) |
| `created_at` | Timestamp |

**Full audit query for a ticket:**

```sql
SELECT
  e.state,
  e.tool_id,
  e.exit_code,
  e.pr_url,
  e.created_at
FROM executions e
JOIN intents i ON i.id = e.intent_id
WHERE i.source_ticket_id = 'PAY-1042'
ORDER BY e.created_at;
```
