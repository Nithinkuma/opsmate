from __future__ import annotations
from . import output
from .providers.base import BaseProvider, ToolCall
from .tools import Tool, get_tool

SYSTEM_PROMPT = """You are OpsMate, an expert Kubernetes operations assistant embedded in a CLI.

Your job: diagnose and resolve Kubernetes issues efficiently.

When investigating:
1. Start with a broad look (get pods/deployments/events) to orient yourself
2. Zoom in on failing resources (describe, logs)
3. Check events for recent warnings
4. Reason about the root cause before suggesting fixes
5. Propose concrete fixes with exact commands or manifests

Principles:
- Be concise. Don't repeat information already shown.
- Use multiple tool calls in one reasoning step when it makes sense.
- Prefer non-destructive actions (dry-run, describe, logs) before mutations.
- Always confirm before applying changes if the user hasn't asked for it.
- Format your final answer in clear markdown with sections when appropriate.
"""


class Agent:
    def __init__(
        self,
        provider: BaseProvider,
        tools: list[Tool],
        max_iterations: int = 20,
        verbose: bool = False,
    ) -> None:
        self.provider = provider
        self.tools = tools
        self.max_iterations = max_iterations
        self.verbose = verbose
        self._messages: list[dict] = []
        self._total_in = 0
        self._total_out = 0

    def run(self, query: str) -> str:
        self._messages.append({"role": "user", "content": query})
        tool_schemas = self.provider.build_tool_schemas(self.tools)

        for iteration in range(self.max_iterations):
            with output.spinner("Thinking..."):
                response = self.provider.chat(self._messages, tool_schemas, SYSTEM_PROMPT)

            self._total_in += response.input_tokens
            self._total_out += response.output_tokens
            self.provider.append_response(self._messages, response)

            if not response.tool_calls:
                return response.content

            results: list[tuple[ToolCall, str]] = []
            for call in response.tool_calls:
                output.print_tool_call(call.name, call.arguments)
                result = self._execute(call)
                if self.verbose:
                    output.print_tool_result(result)
                results.append((call, result))

            self.provider.append_tool_results(self._messages, results)

        return response.content if response.content else "Reached maximum iterations."

    def _execute(self, call: ToolCall) -> str:
        t = get_tool(call.name)
        if t is None:
            return f"Unknown tool: {call.name}"
        try:
            return str(t.func(**call.arguments))
        except Exception as e:
            return f"Tool error: {e}"

    @property
    def token_usage(self) -> tuple[int, int]:
        return self._total_in, self._total_out

    def reset(self) -> None:
        self._messages = []
        self._total_in = 0
        self._total_out = 0
