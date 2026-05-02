# Opsmate

Opsmate is an AI-powered operations assistant that turns routine Jira tickets
into merged pull requests — automatically.

You describe what needs changing in a ticket ("bump lodash to 4.17.21 in
payments-service"), and Opsmate extracts a structured intent, resolves the
right tool, runs it in an isolated sandbox, and opens a PR for human review.

---

## Quickstart

```bash
# 1. Set required environment variables
export ANTHROPIC_API_KEY=sk-ant-...
export JIRA_MCP_TOKEN=...
export BITBUCKET_MCP_TOKEN=...
export DATABASE_URL=postgres://...

# 2. Build
go build -o bin/opsmate ./cmd/cli

# 3. Process a ticket
./bin/opsmate process-ticket PAY-1042
```

The agent will:
1. Fetch the Jira ticket via MCP.
2. Extract a structured intent (verb + parameters + target repo).
3. Find or generate a tool for that intent.
4. Execute the tool in a sandboxed Kubernetes Job.
5. Open a pull request with the diff as its body.
6. Transition the Jira ticket to `In Review`.

---

## Architecture

Full details: [docs/architecture.md](docs/architecture.md)

### Fast path (tool already exists)

```
Jira → Intent Agent → Resolver → Executor → PR Raiser → Jira closed
```

The Intent Agent makes one LLM call to extract a verb + parameters.  The
Resolver looks up the matching tool in the Registry.  The Executor runs the
tool's script in an isolated k8s Job and captures the diff.  The PR Raiser
opens a pull request.  End-to-end latency is typically 5–15 seconds.

### Slow path (no tool yet — tool generation)

```
Resolver (miss) → Generator → tools-registry PR → human merge → Indexer → Registry
```

When no tool exists for the `(verb, repo)` combination, the Generator uses an
LLM to produce a `manifest.yaml` + `script.py` and opens a PR to the
`tools-registry` repository.  After a human approves and merges, the Indexer
hashes and loads the tool into the database.  The original ticket is
automatically retried on the fast path.

---

## Key Concepts

| Term | Meaning |
|---|---|
| **Intent** | Structured JSON extracted from a ticket: verb + parameters + target repo |
| **Verb** | The action type, e.g. `update_dependency`, `update_image_tag` |
| **Tool** | A `(verb, repo-pattern)` implementation: a manifest + a script |
| **Sandbox** | Isolated k8s Job in `agent-sandbox` namespace (PSA restricted, no network) |
| **Registry** | Postgres table of hashed, validated tools |
| **Indexer** | Service that watches the tools-registry repo and loads new/updated tools |

---

## Repository Layout

```
cmd/
  agent/        main entry point for the agent server
  cli/          opsmate CLI (process-ticket, promote-tool, …)
  indexer/      Indexer service
deploy/
  k8s/          Kubernetes manifests (namespace, RBAC)
  docker/       Dockerfiles (agent + runtime images)
docs/           Architecture, operating guide, threat model, verb authoring
eval/
  golden/       Golden test cases (intent + expected diff)
  replay/       Go test runner for golden cases
pkg/
  config/       Configuration loading
  mcp/          Jira and Bitbucket MCP clients
  store/        Postgres access (intents, executions, llm_traces)
  verbs/        Verb schema registry
schemas/        intent.v1.schema.json
tools-registry-seed/
  verbs/        JSON Schemas for each verb
  tools/        Hand-authored tool implementations
```

---

## Documentation

- [Architecture](docs/architecture.md) — pipeline diagrams and cost profiles
- [Adding a Verb](docs/adding-a-verb.md) — how to add a new verb and tool
- [Operating](docs/operating.md) — alerts, traces, audit log, runbooks
- [Threat Model](docs/threat-model.md) — security analysis

---

## Contributing

1. Fork the repo and create a feature branch.
2. Run `make test` to ensure all tests pass.
3. Follow [Adding a Verb](docs/adding-a-verb.md) when introducing new verbs.
4. Open a PR; the CI pipeline must be green before merge.
