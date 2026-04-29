from .base import BaseProvider, Response, ToolCall
from .anthropic import AnthropicProvider
from .openai import OpenAIProvider

__all__ = ["BaseProvider", "Response", "ToolCall", "AnthropicProvider", "OpenAIProvider"]


def make_provider(config) -> BaseProvider:
    key = config.api_key()
    if not key:
        from ..config import PROVIDERS
        env_var = PROVIDERS.get(config.provider, {}).get("env_key", "?")
        raise RuntimeError(
            f"No API key found for provider '{config.provider}'. "
            f"Set the {env_var} environment variable."
        )

    if config.provider == "anthropic":
        return AnthropicProvider(api_key=key, model=config.model)
    if config.provider == "openai":
        return OpenAIProvider(api_key=key, model=config.model)
    raise RuntimeError(f"Unknown provider: {config.provider!r}")
