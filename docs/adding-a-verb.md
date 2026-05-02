# Adding a New Verb

A "verb" is the action half of an intent (e.g. `update_dependency`,
`update_image_tag`).  Adding one requires touching three places: the verb
schema, the seed tools directory, and the embedded schema copy used by the
Go binary.

---

## Step 1 — Write the verb JSON Schema

Create `tools-registry-seed/verbs/{verb}.schema.json`.

The file must be a valid JSON Schema (draft 2020-12) with at minimum:

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://github.com/nithinkuma/opsmate/verbs/{verb}.schema.json",
  "title": "{Verb}",
  "type": "object",
  "required": ["<required_param>"],
  "additionalProperties": false,
  "properties": {
    "<required_param>": { "type": "string" }
  }
}
```

Add the new verb name to the `enum` list in `schemas/intent.v1.schema.json`
under `action.verb`.

---

## Step 2 — Copy the schema into pkg/verbs/schemas/

The Go binary embeds schemas from `pkg/verbs/schemas/`.  Copy the file:

```bash
cp tools-registry-seed/verbs/{verb}.schema.json pkg/verbs/schemas/
```

Both copies must be kept in sync.  The seed copy is the canonical source;
the pkg copy is embedded at compile time.

---

## Step 3 — Write a hand-coded tool

Create a directory:

```
tools-registry-seed/tools/{org}__{repo}/{verb}/
  manifest.yaml
  script.py          # or script.sh / main.go
  golden_tests/
    case_1.input.json
```

**manifest.yaml** fields to fill in:

```yaml
id: {org}__{repo}.{verb}
verb: {verb}
schema_version: 1

repo:
  pattern: "{org}/{repo}"
  default_branch: main

runtime:
  language: python          # python | bash | go
  entrypoint: ./script.py
  interpreter: python3.11
  dependencies: []          # pinned pip packages if any

preconditions:
  - "file_exists: <file>"

postconditions:
  - "valid_json: <file>"    # or valid_yaml

provenance:
  generated_by: hand
  generated_at: "2026-01-01T00:00:00Z"

hash:
  manifest: "sha256:placeholder"   # filled in by make hash
  script:   "sha256:placeholder"
```

**script.py** contract:

- Read `$PARAMS_PATH` (JSON) for verb parameters.
- Read/modify files under `$REPO_PATH`.
- Write a unified diff to stdout.
- Exit 0 on success (including when already at desired state — idempotent).
- Exit 1 on failure, with a message to stderr.
- Never touch the network.

**case_1.input.json** — a representative input for the happy path:

```json
{ "<param>": "<value>" }
```

---

## Step 4 — Run the tests

```bash
make test
```

This runs:

- `go test ./...` — unit tests including verb schema validation.
- `go test -run TestGoldenCases ./eval/replay/` — replay tests against
  `eval/golden/*.json`.

Fix any failures before proceeding.

---

## Step 5 — Commit and push to tools-registry

```bash
git add tools-registry-seed/tools/{org}__{repo}/{verb}/ \
        tools-registry-seed/verbs/{verb}.schema.json \
        pkg/verbs/schemas/{verb}.schema.json \
        schemas/intent.v1.schema.json
git commit -m "feat(verbs): add {verb} verb and {org}/{repo} tool"
git push origin main
```

The Indexer watches the `tools-registry` repository.  Once the PR is merged
(or on a direct push to `main` in development), the Indexer will hash and
load the new tool into the Registry database, making it available on the fast
path immediately.

---

## Checklist

- [ ] `tools-registry-seed/verbs/{verb}.schema.json` created
- [ ] `pkg/verbs/schemas/{verb}.schema.json` matches the above
- [ ] Verb name added to `schemas/intent.v1.schema.json` enum
- [ ] `manifest.yaml` passes schema validation (`make lint`)
- [ ] `script.py` exits 0 when run against golden input
- [ ] `script.py` is idempotent (second run produces empty diff)
- [ ] `make test` passes
- [ ] PR reviewed and merged into tools-registry
