from __future__ import annotations
from abc import ABC, abstractmethod
from dataclasses import dataclass, field
from typing import Any


@dataclass
class ToolCall:
    id: str
    name: str
    arguments: dict[str, Any]


@dataclass
class Response:
    content: str
    tool_calls: list[ToolCall] = field(default_factory=list)
    input_tokens: int = 0
    output_tokens: int = 0
    _raw: Any = field(default=None, repr=False, compare=False)


class BaseProvider(ABC):
    @abstractmethod
    def chat(self, messages: list[dict], tool_schemas: list[dict], system: str = "") -> Response: ...

    @abstractmethod
    def append_response(self, messages: list[dict], response: Response) -> None:
        """Append the assistant turn to messages in provider-native format."""
        ...

    @abstractmethod
    def append_tool_results(self, messages: list[dict], results: list[tuple[ToolCall, str]]) -> None:
        """Append tool results to messages in provider-native format."""
        ...

    @abstractmethod
    def build_tool_schemas(self, tools: list) -> list[dict]: ...

    @property
    @abstractmethod
    def label(self) -> str: ...
