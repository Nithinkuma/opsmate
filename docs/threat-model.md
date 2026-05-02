# Threat Model

This document enumerates the primary threat scenarios for Opsmate, the controls
that mitigate each one, and any residual risk.

---

## 1. Compromised Tool Script

**Threat:** An attacker modifies a `script.py` in the tools-registry repository
(via a compromised committer account or a malicious PR) and the agent executes
the modified script.

**Impact:** Arbitrary code execution in the sandbox; potential data exfiltration
through the diff output channel; repo contents exposed to the script.

**Mitigations:**

- **Hash verification at load time:** The Indexer computes `sha256` of both
  `manifest.yaml` and `script.py` when ingesting a tool.  The Executor verifies
  the hash against the DB record before launching the Job.  A modified script
  will not run unless a DB record has also been updated.
- **Sandbox isolation (PSA restricted):** The Job runs in the `agent-sandbox`
  namespace with `pod-security.kubernetes.io/enforce: restricted`.  This
  enforces: no privileged containers, no host path mounts, read-only root
  filesystem, dropped capabilities, non-root UID.
- **No network access:** The Job's network policy blocks all egress.  The
  script cannot phone home or exfiltrate data via HTTP.

**Residual risk:** A compromised DB record in addition to a compromised script
would bypass hash verification.  DB access is controlled separately; see
credential leakage (§5).

---

## 2. Prompt Injection from Jira Ticket

**Threat:** An attacker crafts a Jira ticket whose description contains an
instruction that causes the Intent Agent (LLM) to produce a malicious intent
JSON — for example, targeting a different repo or injecting shell commands into
parameters.

**Impact:** Unintended action taken against a repo the ticket author should not
be able to target.

**Mitigations:**

- **Constrained verb enum:** The intent JSON schema (`schemas/intent.v1.schema.json`)
  declares a closed `enum` for `action.verb`.  Any verb outside the list fails
  JSON Schema validation and is rejected before the Resolver is invoked.
- **JSON Schema validation on every intent:** The Intent Agent's output is
  validated against the schema before any downstream processing.  Parameter
  values are validated against the per-verb schema.  Injection of shell
  metacharacters in parameter strings does not affect execution because scripts
  receive parameters as a JSON file (`$PARAMS_PATH`), not via shell argument
  expansion.
- **Repo scoping:** The Resolver only returns tools whose `repo.pattern` matches
  the intent's `target.repo`.  The LLM cannot target an arbitrary repo by
  choosing a tool — the tool must already exist for that repo.

**Residual risk:** A sufficiently sophisticated injection might persuade the LLM
to produce a valid-but-harmful intent (e.g. deleting a dependency rather than
adding one) if a verb with destructive semantics were added in the future.
Reviews of new verb schemas should assess this.

---

## 3. Malicious PR to tools-registry

**Threat:** An attacker opens a PR to the tools-registry repository containing
a malicious `script.py` that passes superficial code review.

**Impact:** Once merged and indexed, the script would be executed against real
repos in the sandbox.

**Mitigations:**

- **Human review gate:** All PRs to tools-registry require at least one human
  approval before merging.  The Indexer only ingests tools from the `main`
  branch after merge.
- **Hash verification on load:** Hashes are computed from the merged state, so
  any post-merge modification would produce a `hash_mismatch` alert (§ operating.md).
- **Sandbox isolation:** Even if a malicious script is executed, it operates
  in a network-isolated, capability-dropped container with no credentials.

**Residual risk:** Social engineering of a reviewer.  Mitigated by requiring
two reviewers for new tool additions (configurable via branch protection).

---

## 4. Sandbox Escape

**Threat:** A tool script exploits a container runtime vulnerability to escape
the sandbox namespace and access cluster internals.

**Impact:** Access to secrets, other namespaces, or the control plane.

**Mitigations:**

- **PSA restricted profile:** Enforces `seccompProfile: RuntimeDefault`,
  `allowPrivilegeEscalation: false`, all capabilities dropped, non-root UID.
- **Read-only root filesystem:** The container cannot write to system paths.
- **No service account token mounted:** Job pods do not receive a k8s API token.
- **Network policy:** Egress to the k8s API server is blocked.
- **Namespace isolation:** Even a container escape would land in `agent-sandbox`,
  which has no cluster-wide RBAC.

**Residual risk:** A zero-day in the container runtime or kernel.  Mitigated by
keeping node OS and runtime patched and by using distroless / minimal base
images.

---

## 5. Credential Leakage

**Threat:** A component is compromised and its credentials are used to cause
harm — e.g. the Intent Agent's Bitbucket token is stolen and used to push code.

**Impact:** Depends on which component; at worst, arbitrary repo writes.

**Mitigations:**

- **Credential segmentation:** No single component holds all credentials
  (see architecture trust boundary diagram).  The sandbox Job holds zero
  credentials.  The Indexer holds only a read token for tools-registry.
- **Least-privilege tokens:** Each token is scoped to the minimum required
  (e.g. the Bitbucket token for the PR Raiser can only open PRs, not merge
  them on protected branches).
- **Short-lived credentials:** Where possible, tokens are rotated via the
  `rotate_secret_ref` verb itself (eating our own dog food).

**Residual risk:** Compromise of the Intent Agent process would expose the
Anthropic key and Bitbucket token.  Rotate immediately if that process is
suspected compromised.

---

## 6. LLM Hallucinated Verb

**Threat:** The Intent Agent produces an intent with a verb that does not exist
in the registry (e.g. `delete_all_files`) because the model hallucinated it.

**Impact:** If accepted, the Resolver would fail to find a tool, but the system
could potentially be confused into unsafe behaviour.

**Mitigations:**

- **Closed enum validation:** The `action.verb` field is a JSON Schema `enum`.
  Any value outside the defined list causes schema validation to fail
  immediately.  The system is fail-closed: an unknown verb produces an error
  and parks the intent in `FAILED` state with an alert; it does not attempt
  execution.
- **Verb registry double-check:** Even if validation were bypassed, the Resolver
  performs an independent lookup in the verb registry.  An unknown verb returns
  `unknown verb — not in verb registry` and aborts.

**Residual risk:** Negligible once the enum is maintained correctly.  New verbs
must be added deliberately (see [Adding a Verb](adding-a-verb.md)).
