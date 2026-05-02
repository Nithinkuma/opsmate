# Architecture

## Overview

Opsmate automates routine infrastructure changes end-to-end: it reads a Jira
ticket, extracts a structured intent, resolves the right tool, executes it in
an isolated sandbox, raises a pull request, and closes the ticket.  The system
has two operational paths depending on whether a matching tool already exists.

---

## Fast Path (cache hit)

```
  ┌─────────┐     ┌──────────────┐     ┌──────────┐     ┌──────────┐     ┌──────────────┐     ┌─────────┐
  │  Jira   │────▶│ Intent Agent │────▶│ Resolver │────▶│ Executor │────▶│  PR Raiser   │────▶│  Jira   │
  │ ticket  │     │  (LLM)       │     │          │     │ (sandbox)│     │ (Bitbucket)  │     │ closed  │
  └─────────┘     └──────────────┘     └──────────┘     └──────────┘     └──────────────┘     └─────────┘
       │                  │                  │                │                   │
       │    fetch via      │  produce Intent  │ look up tool   │ run Job in        │ open PR,
       │    Jira MCP       │  JSON (verb +    │ by (verb,repo) │ agent-sandbox ns  │ set approvers
       │                   │  params + target)│ from Registry  │                  │
       ▼                   ▼                  ▼                ▼                   ▼
   raw ticket          intent.v1.json    manifest.yaml     diff on stdout      PR URL stored
                       validated against  + script.py      hash verified       in executions
                       JSON Schema        hash verified     before exec         table
```

**Latency budget:** ~5–15 s end-to-end (one LLM call + one sandbox Job).

---

## Slow Path (cache miss → tool generation)

```
  ┌────────────┐     ┌───────────┐     ┌───────────────────┐     ┌────────────┐     ┌──────────┐
  │  Resolver  │────▶│ Generator │────▶│ tools-registry PR │────▶│  Indexer   │────▶│ Registry │
  │ (cache miss│     │  (LLM)    │     │  awaiting review  │     │            │     │  (DB)    │
  └────────────┘     └───────────┘     └───────────────────┘     └────────────┘     └──────────┘
        │                  │                      │                      │
        │  no tool for      │ generate manifest.yaml│ human reviews diff  │ on merge, Indexer
        │  (verb, repo)     │ + script.py, open PR  │ + approves/rejects  │ hashes + inserts
        │                   │ to tools-registry     │                     │ into tools table
        ▼                   ▼                       ▼                     ▼
   escalation alert     Generator span          PR comment audit      tool available for
   raised in OTEL        in llm_traces           trail                future fast-path
```

**Latency budget:** minutes to hours (depends on human review turnaround).

The original ticket is parked with status `WAITING_TOOL`; the agent resumes
once the Indexer fires a notification.

---

## Trust Boundary Diagram

```
  ╔══════════════════════════════════════════════════════════════════╗
  ║  opsmate-system namespace                                        ║
  ║                                                                  ║
  ║  ┌──────────────┐   Jira token    ┌──────────────┐              ║
  ║  │ Intent Agent │────────────────▶│  Jira MCP    │              ║
  ║  │              │   Bitbucket tok ▶  Bitbucket   │              ║
  ║  │              │   Anthropic key ▶  LLM API     │              ║
  ║  └──────┬───────┘                 └──────────────┘              ║
  ║         │ k8s Jobs API (RBAC)                                    ║
  ╠═════════╪════════════════════════════════════════════════════════╣
  ║  agent-sandbox namespace (PSA: restricted)                       ║
  ║         │                                                        ║
  ║         ▼                                                        ║
  ║  ┌──────────────┐                                                ║
  ║  │  Tool Job    │  ← no network, no credentials, read-only FS    ║
  ║  │  (script.py) │    write allowed only to /workspace/out        ║
  ║  └──────────────┘                                                ║
  ╚══════════════════════════════════════════════════════════════════╝

  ┌──────────────┐
  │   Indexer    │  holds: Bitbucket token (read tools-registry only)
  └──────────────┘         DB write credentials
```

**Key principle:** no single component holds all credentials.  The sandbox Job
holds none.

---

## Component Responsibilities

| Component | What it does | Credentials held |
|---|---|---|
| Intent Agent | LLM call → Intent JSON | Jira token, Anthropic key, Bitbucket token |
| Resolver | look up tool in Registry DB | DB read |
| Executor | create k8s Job, collect diff | k8s SA (agent-sandbox only) |
| PR Raiser | open Bitbucket PR | Bitbucket token |
| Generator | LLM call → tool scripts | Anthropic key, Bitbucket token (tools-registry) |
| Indexer | watch tools-registry PRs → DB | Bitbucket token, DB write |

---

## Cost Profiles

### Fast path

- **LLM tokens:** ~1 000–3 000 input tokens (ticket + schema) + ~500 output tokens.
- **Compute:** one short-lived k8s Job (< 30 s CPU, < 128 MiB RAM).
- **Marginal cost per ticket:** < $0.01 at current model pricing.

### Slow path

- **LLM tokens:** ~8 000–20 000 tokens (few-shot examples + schema + generation).
- **Human time:** 5–15 min review per PR.
- **Amortisation:** the generated tool is reused for all future identical
  (verb, repo) pairs, so cost per ticket drops to fast-path levels after the
  first execution.
