from __future__ import annotations
import inspect
import importlib.util
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Callable, get_args, get_origin, Union

_registry: dict[str, "Tool"] = {}


@dataclass
class Tool:
    name: str
    description: str
    parameters: dict
    func: Callable


def tool(description: str, params: dict[str, str] | None = None):
    """Decorator that registers a function as an OpsMate tool.

    Usage:
        @tool("Short description", params={"arg": "What this arg does"})
        def my_tool(arg: str) -> str:
            ...
    """
    def decorator(func: Callable) -> Callable:
        sig = inspect.signature(func)
        properties: dict[str, dict] = {}
        required: list[str] = []

        for pname, param in sig.parameters.items():
            prop = _annotation_to_schema(param.annotation)
            if params and pname in params:
                prop["description"] = params[pname]
            properties[pname] = prop
            if param.default is inspect.Parameter.empty:
                required.append(pname)

        schema: dict[str, Any] = {"type": "object", "properties": properties}
        if required:
            schema["required"] = required

        _registry[func.__name__] = Tool(
            name=func.__name__,
            description=description,
            parameters=schema,
            func=func,
        )
        return func

    return decorator


def _annotation_to_schema(ann: Any) -> dict:
    origin = get_origin(ann)
    if origin is Union:
        inner = [a for a in get_args(ann) if a is not type(None)]
        return _annotation_to_schema(inner[0]) if inner else {"type": "string"}
    if ann is int:
        return {"type": "integer"}
    if ann is bool:
        return {"type": "boolean"}
    if ann is list or origin is list:
        return {"type": "array", "items": {"type": "string"}}
    return {"type": "string"}


def get_all_tools() -> list[Tool]:
    return list(_registry.values())


def get_tool(name: str) -> Tool | None:
    return _registry.get(name)


def load_user_tools() -> int:
    """Load tools from ~/.opsmate/tools/*.py — return count loaded."""
    tools_dir = Path.home() / ".opsmate" / "tools"
    if not tools_dir.exists():
        return 0

    loaded = 0
    for path in sorted(tools_dir.glob("*.py")):
        try:
            spec = importlib.util.spec_from_file_location(path.stem, path)
            if spec and spec.loader:
                mod = importlib.util.module_from_spec(spec)
                # Inject the tool decorator so user files can just use `tool`
                mod.tool = tool  # type: ignore[attr-defined]
                spec.loader.exec_module(mod)  # type: ignore[union-attr]
                loaded += 1
        except Exception:
            pass
    return loaded
