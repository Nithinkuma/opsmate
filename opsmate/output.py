from __future__ import annotations
from contextlib import contextmanager
from rich.console import Console
from rich.markdown import Markdown
from rich.panel import Panel
from rich.text import Text
from rich.table import Table
from rich import box

console = Console(highlight=False)


def print_answer(text: str) -> None:
    console.print(Panel(
        Markdown(text),
        border_style="bright_green",
        title="[bold bright_green]OpsMate[/bold bright_green]",
        title_align="left",
        padding=(1, 2),
    ))


def print_tool_call(name: str, args: dict) -> None:
    parts = []
    for k, v in args.items():
        v_str = str(v)
        if len(v_str) > 60:
            v_str = v_str[:57] + "..."
        parts.append(f"[cyan]{k}[/cyan]=[yellow]{v_str}[/yellow]")
    args_display = "  ".join(parts)
    console.print(f"  [bold blue]▶[/bold blue] [dim]{name}[/dim]  {args_display}")


def print_tool_result(result: str, max_lines: int = 6) -> None:
    lines = result.strip().splitlines()
    preview = lines[:max_lines]
    omitted = len(lines) - max_lines
    display = "\n".join(preview)
    if omitted > 0:
        display += f"\n  [dim]... {omitted} more lines[/dim]"
    console.print(f"    [dim]{display}[/dim]")


def print_error(msg: str) -> None:
    console.print(f"[bold red]Error:[/bold red] {msg}")


def print_warning(msg: str) -> None:
    console.print(f"[bold yellow]Warning:[/bold yellow] {msg}")


def print_info(msg: str) -> None:
    console.print(f"[dim]{msg}[/dim]")


def print_token_usage(input_tokens: int, output_tokens: int) -> None:
    console.print(
        f"[dim]  tokens: {input_tokens:,} in / {output_tokens:,} out[/dim]",
        justify="right",
    )


def print_providers_table(providers: dict) -> None:
    table = Table(box=box.SIMPLE, show_header=True, header_style="bold")
    table.add_column("Provider", style="cyan")
    table.add_column("Model", style="white")
    table.add_column("Notes", style="dim")
    for provider_name, info in providers.items():
        for i, (model, desc) in enumerate(info["models"].items()):
            p_label = provider_name if i == 0 else ""
            table.add_row(p_label, model, desc)
    console.print(table)


def print_tools_table(tools: list) -> None:
    table = Table(box=box.SIMPLE, show_header=True, header_style="bold")
    table.add_column("Tool", style="cyan")
    table.add_column("Description", style="white")
    table.add_column("Parameters", style="dim")
    for t in sorted(tools, key=lambda x: x.name):
        params = ", ".join(t.parameters.get("properties", {}).keys())
        table.add_row(t.name, t.description[:60], params)
    console.print(table)


@contextmanager
def spinner(msg: str):
    with console.status(f"[dim]{msg}[/dim]", spinner="dots"):
        yield
