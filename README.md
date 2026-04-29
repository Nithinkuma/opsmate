# OpsMate

AI-powered Kubernetes operations assistant — debug issues, run health checks, and apply fixes from your terminal.

## Features

- **K8s debugging** — automatically investigates pods, logs, events, and deployments
- **Multi-provider** — works with Anthropic (Claude) and OpenAI (GPT-4o, o3)
- **Extensible tools** — add your own tools by dropping a `.py` file in `~/.opsmate/tools/`
- **Clean output** — only shows what matters; no noise

## Install

```bash
pip install -e .
```

## Setup

Set your API key for whichever provider you want to use:

```bash
export ANTHROPIC_API_KEY=sk-ant-...   # Anthropic Claude (default)
export OPENAI_API_KEY=sk-...          # OpenAI GPT
```

## Usage

```bash
# Ask a free-form question — OpsMate uses kubectl tools to investigate
opsmate ask "why is my pod crashing in the payments namespace?"

# Debug a specific resource
opsmate debug pod/nginx-7d4b8c9f6-xxxx -n production

# Full cluster health check
opsmate diagnose
opsmate diagnose -n staging

# Interactive multi-turn session
opsmate chat

# Use a specific provider / model
opsmate ask "show me failing pods" --provider openai --model gpt-4o
opsmate ask "show me failing pods" --provider anthropic --model claude-opus-4-7
```

## Configuration

```bash
# View current config
opsmate config show

# Persist provider/model defaults
opsmate config set provider anthropic
opsmate config set model claude-opus-4-7

# See all providers and models
opsmate config providers
```

## Providers & Models

| Provider  | Model                     | Notes                              |
|-----------|---------------------------|------------------------------------|
| anthropic | claude-opus-4-7           | Most capable, best for complex debugging |
| anthropic | claude-sonnet-4-6         | Balanced speed & quality (default) |
| anthropic | claude-haiku-4-5-20251001 | Fastest, lowest cost               |
| openai    | o3                        | Best reasoning                     |
| openai    | gpt-4o                    | Balanced                           |
| openai    | gpt-4o-mini               | Fast & cheap                       |

## Adding Custom Tools

Drop a Python file in `~/.opsmate/tools/`:

```python
# ~/.opsmate/tools/my_tools.py
# `tool` is injected automatically — no imports needed

@tool(
    "Check disk usage on the local machine",
    params={"path": "Path to check (default: /)"}
)
def disk_usage(path: str = "/") -> str:
    import subprocess
    r = subprocess.run(["df", "-h", path], capture_output=True, text=True)
    return r.stdout
```

Tools are picked up automatically on the next `opsmate` invocation. View registered tools:

```bash
opsmate tools list
opsmate tools add-example   # creates a starter template
```

## Built-in K8s Tools

| Tool | Purpose |
|------|---------|
| `kubectl_get` | List any resource type |
| `kubectl_describe` | Full resource details |
| `kubectl_logs` | Pod/container logs |
| `kubectl_events` | Cluster events (sorted by time) |
| `kubectl_top` | CPU/memory usage |
| `kubectl_exec` | Run commands inside pods |
| `kubectl_rollout_restart` | Rolling restart |
| `kubectl_rollout_undo` | Roll back a deployment |
| `kubectl_scale` | Change replica count |
| `kubectl_set_image` | Update container image |
| `kubectl_apply` | Apply YAML (dry-run by default) |
| `kubectl_delete` | Delete a resource |
| `kubectl_get_contexts` | List kubeconfig contexts |
| `kubectl_use_context` | Switch context |
| `kubectl_cluster_info` | Cluster endpoint info |
| `kubectl_node_status` | Node health |
| `run_shell` | Arbitrary shell command |
