from __future__ import annotations
import subprocess
from . import tool

_MAX_OUTPUT = 8_000


@tool(
    "Run an arbitrary shell command on the local machine — use sparingly for tasks kubectl can't do",
    params={
        "command": "Shell command to execute",
        "timeout": "Timeout in seconds (default 30)",
    },
)
def run_shell(command: str, timeout: str = "30") -> str:
    try:
        r = subprocess.run(
            command, shell=True, capture_output=True, text=True,
            timeout=int(timeout),
        )
        out = (r.stdout + r.stderr).strip()
        if len(out) > _MAX_OUTPUT:
            out = "[... truncated ...]\n" + out[-_MAX_OUTPUT:]
        return out or "(no output)"
    except subprocess.TimeoutExpired:
        return f"Error: command timed out after {timeout}s"
    except Exception as e:
        return f"Error: {e}"
