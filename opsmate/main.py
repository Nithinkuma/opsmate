from __future__ import annotations
import sys
import typer
from typing import Optional
from rich.console import Console

from .config import load_config, save_config, PROVIDERS, Config
from . import output

# Eagerly import tools so they register themselves
from .tools import get_all_tools, load_user_tools
from .tools import k8s as _k8s  # noqa: F401
from .tools import shell as _shell  # noqa: F401

app = typer.Typer(
    name="opsmate",
    help="OpsMate — AI-powered Kubernetes operations assistant",
    no_args_is_help=True,
    rich_markup_mode="rich",
)
config_app = typer.Typer(help="Manage OpsMate configuration", no_args_is_help=True)
tools_app = typer.Typer(help="Manage OpsMate tools", no_args_is_help=True)
app.add_typer(config_app, name="config")
app.add_typer(tools_app, name="tools")

err = Console(stderr=True)


def _build_agent(cfg: Config, verbose: bool = False):
    from .providers import make_provider
    from .agent import Agent
    try:
        provider = make_provider(cfg)
    except RuntimeError as e:
        output.print_error(str(e))
        raise typer.Exit(1)

    load_user_tools()
    tools = get_all_tools()
    return Agent(provider=provider, tools=tools, max_iterations=cfg.max_iterations, verbose=verbose)


# ── ask ──────────────────────────────────────────────────────────────────────

@app.command()
def ask(
    query: str = typer.Argument(..., help="Question or task for OpsMate"),
    provider: Optional[str] = typer.Option(None, "--provider", "-p", help="Override provider (anthropic|openai)"),
    model: Optional[str] = typer.Option(None, "--model", "-m", help="Override model name"),
    verbose: bool = typer.Option(False, "--verbose", "-v", help="Show tool output previews"),
    no_usage: bool = typer.Option(False, "--no-usage", help="Hide token usage summary"),
):
    """Ask OpsMate a question. It will use k8s tools to investigate and respond."""
    cfg = load_config()
    if provider:
        cfg.provider = provider
    if model:
        cfg.model = model

    agent = _build_agent(cfg, verbose=verbose)
    answer = agent.run(query)
    output.print_answer(answer)
    if not no_usage:
        i, o = agent.token_usage
        if i or o:
            output.print_token_usage(i, o)


# ── debug ────────────────────────────────────────────────────────────────────

@app.command()
def debug(
    resource: str = typer.Argument(..., help="Resource to debug, e.g. pod/nginx-xxx or deployment/api"),
    namespace: str = typer.Option("default", "--namespace", "-n", help="Kubernetes namespace"),
    provider: Optional[str] = typer.Option(None, "--provider", "-p"),
    model: Optional[str] = typer.Option(None, "--model", "-m"),
    verbose: bool = typer.Option(False, "--verbose", "-v"),
):
    """Debug a specific Kubernetes resource — OpsMate investigates and diagnoses it."""
    cfg = load_config()
    if provider:
        cfg.provider = provider
    if model:
        cfg.model = model

    query = (
        f"Debug the Kubernetes resource '{resource}' in namespace '{namespace}'. "
        "Investigate its status, logs, and recent events. "
        "Identify the root cause of any issues and suggest concrete fixes."
    )
    agent = _build_agent(cfg, verbose=verbose)
    answer = agent.run(query)
    output.print_answer(answer)
    i, o = agent.token_usage
    if i or o:
        output.print_token_usage(i, o)


# ── diagnose ─────────────────────────────────────────────────────────────────

@app.command()
def diagnose(
    namespace: Optional[str] = typer.Option(None, "--namespace", "-n", help="Namespace to focus on (default: all)"),
    provider: Optional[str] = typer.Option(None, "--provider", "-p"),
    model: Optional[str] = typer.Option(None, "--model", "-m"),
    verbose: bool = typer.Option(False, "--verbose", "-v"),
):
    """Run a full cluster health check — checks nodes, pods, deployments, and events."""
    cfg = load_config()
    if provider:
        cfg.provider = provider
    if model:
        cfg.model = model

    if namespace:
        scope = f"in namespace '{namespace}'"
    else:
        scope = "across all namespaces"

    query = (
        f"Perform a comprehensive health check of the Kubernetes cluster {scope}. "
        "Check node status, identify any pods that are not Running/Completed, "
        "look for recent Warning events, check for resource pressure, "
        "and summarize any issues found with recommended actions."
    )
    agent = _build_agent(cfg, verbose=verbose)
    answer = agent.run(query)
    output.print_answer(answer)
    i, o = agent.token_usage
    if i or o:
        output.print_token_usage(i, o)


# ── chat ─────────────────────────────────────────────────────────────────────

@app.command()
def chat(
    provider: Optional[str] = typer.Option(None, "--provider", "-p"),
    model: Optional[str] = typer.Option(None, "--model", "-m"),
    verbose: bool = typer.Option(False, "--verbose", "-v"),
):
    """Start an interactive multi-turn session with OpsMate."""
    from rich.prompt import Prompt
    cfg = load_config()
    if provider:
        cfg.provider = provider
    if model:
        cfg.model = model

    agent = _build_agent(cfg, verbose=verbose)
    output.console.print(
        f"[bold]OpsMate chat[/bold] [dim]({agent.provider.label})[/dim]  "
        "[dim]Type 'exit' or Ctrl+C to quit.[/dim]\n"
    )

    try:
        while True:
            try:
                query = Prompt.ask("[bold cyan]You[/bold cyan]")
            except (EOFError, KeyboardInterrupt):
                break

            if query.strip().lower() in {"exit", "quit", "q"}:
                break
            if not query.strip():
                continue

            answer = agent.run(query)
            output.print_answer(answer)

    except KeyboardInterrupt:
        pass

    i, o = agent.token_usage
    if i or o:
        output.print_token_usage(i, o)
    output.console.print("\n[dim]Session ended.[/dim]")


# ── config subcommands ────────────────────────────────────────────────────────

@config_app.command("show")
def config_show():
    """Show current OpsMate configuration."""
    cfg = load_config()
    from rich.table import Table
    from rich import box
    t = Table(box=box.SIMPLE, show_header=False)
    t.add_column("Key", style="cyan")
    t.add_column("Value", style="white")
    t.add_row("provider", cfg.provider)
    t.add_row("model", cfg.model)
    t.add_row("max_iterations", str(cfg.max_iterations))
    t.add_row("verbose", str(cfg.verbose))
    key = cfg.api_key()
    masked = (key[:4] + "..." + key[-4:]) if key and len(key) > 8 else ("not set" if not key else "***")
    t.add_row("api_key", masked)
    output.console.print(t)


@config_app.command("set")
def config_set(
    key: str = typer.Argument(..., help="Config key: provider | model | max_iterations | verbose"),
    value: str = typer.Argument(..., help="Value to set"),
):
    """Set a configuration value and save to ~/.opsmate/config.json."""
    cfg = load_config()
    allowed = {"provider", "model", "max_iterations", "verbose"}
    if key not in allowed:
        output.print_error(f"Unknown key '{key}'. Allowed: {', '.join(sorted(allowed))}")
        raise typer.Exit(1)
    try:
        d = cfg.model_dump()
        if key == "max_iterations":
            d[key] = int(value)
        elif key == "verbose":
            d[key] = value.lower() in {"true", "1", "yes"}
        else:
            d[key] = value
        save_config(Config(**d))
        output.console.print(f"[green]Set[/green] {key} = {value}")
    except Exception as e:
        output.print_error(str(e))
        raise typer.Exit(1)


@config_app.command("providers")
def config_providers():
    """List all supported providers and models."""
    output.print_providers_table(PROVIDERS)
    output.console.print(
        "\nSet your provider with:  [cyan]opsmate config set provider anthropic[/cyan]\n"
        "Set your model with:     [cyan]opsmate config set model claude-opus-4-7[/cyan]\n"
        "Set your API key via env: [cyan]ANTHROPIC_API_KEY=sk-... opsmate ask ...[/cyan]"
    )


# ── tools subcommands ─────────────────────────────────────────────────────────

@tools_app.command("list")
def tools_list():
    """List all registered OpsMate tools."""
    load_user_tools()
    tools = get_all_tools()
    if not tools:
        output.print_info("No tools registered.")
        return
    output.print_tools_table(tools)
    output.console.print(
        f"\n[dim]{len(tools)} tools available. "
        "Add custom tools to [bold]~/.opsmate/tools/*.py[/bold][/dim]"
    )


@tools_app.command("add-example")
def tools_add_example():
    """Create an example custom tool file in ~/.opsmate/tools/."""
    from pathlib import Path
    tools_dir = Path.home() / ".opsmate" / "tools"
    tools_dir.mkdir(parents=True, exist_ok=True)
    example = tools_dir / "example_tool.py"
    if example.exists():
        output.print_warning(f"{example} already exists — not overwriting.")
        return

    example.write_text(
        '# Custom OpsMate tool example\n'
        '# This file is auto-loaded at startup.\n'
        '# Use the `tool` decorator (injected automatically) to register functions.\n\n'
        '@tool(\n'
        '    "Check disk usage on the local machine",\n'
        '    params={"path": "Path to check (default: /)"}\n'
        ')\n'
        'def disk_usage(path: str = "/") -> str:\n'
        '    import subprocess\n'
        '    r = subprocess.run(["df", "-h", path], capture_output=True, text=True)\n'
        '    return r.stdout\n'
    )
    output.console.print(f"[green]Created[/green] {example}")
    output.console.print("[dim]Edit it to add your own tools, then run 'opsmate tools list'.[/dim]")


if __name__ == "__main__":
    app()
