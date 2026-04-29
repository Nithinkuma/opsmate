from __future__ import annotations
import json
import openai
from .base import BaseProvider, Response, ToolCall


class OpenAIProvider(BaseProvider):
    def __init__(self, api_key: str, model: str) -> None:
        self._client = openai.OpenAI(api_key=api_key)
        self._model = model

    @property
    def label(self) -> str:
        return f"openai/{self._model}"

    def chat(self, messages: list[dict], tool_schemas: list[dict], system: str = "") -> Response:
        msgs = messages.copy()
        if system:
            msgs = [{"role": "system", "content": system}] + msgs

        kwargs: dict = dict(model=self._model, messages=msgs)
        if tool_schemas:
            kwargs["tools"] = tool_schemas

        resp = self._client.chat.completions.create(**kwargs)
        msg = resp.choices[0].message

        calls: list[ToolCall] = []
        if msg.tool_calls:
            for tc in msg.tool_calls:
                calls.append(ToolCall(
                    id=tc.id,
                    name=tc.function.name,
                    arguments=json.loads(tc.function.arguments),
                ))

        return Response(
            content=msg.content or "",
            tool_calls=calls,
            input_tokens=resp.usage.prompt_tokens,
            output_tokens=resp.usage.completion_tokens,
            _raw=msg,
        )

    def append_response(self, messages: list[dict], response: Response) -> None:
        msg = response._raw
        entry: dict = {"role": "assistant", "content": msg.content or ""}
        if msg.tool_calls:
            entry["tool_calls"] = [
                {
                    "id": tc.id,
                    "type": "function",
                    "function": {"name": tc.function.name, "arguments": tc.function.arguments},
                }
                for tc in msg.tool_calls
            ]
        messages.append(entry)

    def append_tool_results(self, messages: list[dict], results: list[tuple[ToolCall, str]]) -> None:
        for call, result in results:
            messages.append({"role": "tool", "tool_call_id": call.id, "content": result})

    def build_tool_schemas(self, tools: list) -> list[dict]:
        return [
            {
                "type": "function",
                "function": {
                    "name": t.name,
                    "description": t.description,
                    "parameters": t.parameters,
                },
            }
            for t in tools
        ]
