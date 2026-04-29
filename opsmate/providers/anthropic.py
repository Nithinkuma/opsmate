from __future__ import annotations
import anthropic
from .base import BaseProvider, Response, ToolCall


class AnthropicProvider(BaseProvider):
    def __init__(self, api_key: str, model: str) -> None:
        self._client = anthropic.Anthropic(api_key=api_key)
        self._model = model

    @property
    def label(self) -> str:
        return f"anthropic/{self._model}"

    def chat(self, messages: list[dict], tool_schemas: list[dict], system: str = "") -> Response:
        kwargs: dict = dict(model=self._model, max_tokens=8096, messages=messages)
        if tool_schemas:
            kwargs["tools"] = tool_schemas
        if system:
            kwargs["system"] = system

        msg = self._client.messages.create(**kwargs)

        text = ""
        calls: list[ToolCall] = []
        for block in msg.content:
            if block.type == "text":
                text += block.text
            elif block.type == "tool_use":
                calls.append(ToolCall(id=block.id, name=block.name, arguments=block.input))

        return Response(
            content=text,
            tool_calls=calls,
            input_tokens=msg.usage.input_tokens,
            output_tokens=msg.usage.output_tokens,
            _raw=msg,
        )

    def append_response(self, messages: list[dict], response: Response) -> None:
        messages.append({"role": "assistant", "content": response._raw.content})

    def append_tool_results(self, messages: list[dict], results: list[tuple[ToolCall, str]]) -> None:
        content = [
            {"type": "tool_result", "tool_use_id": call.id, "content": result}
            for call, result in results
        ]
        messages.append({"role": "user", "content": content})

    def build_tool_schemas(self, tools: list) -> list[dict]:
        return [
            {"name": t.name, "description": t.description, "input_schema": t.parameters}
            for t in tools
        ]
