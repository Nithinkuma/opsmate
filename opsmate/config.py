from __future__ import annotations
import os
import json
from pathlib import Path
from pydantic import BaseModel

CONFIG_PATH = Path.home() / ".opsmate" / "config.json"

PROVIDERS: dict[str, dict] = {
    "anthropic": {
        "env_key": "ANTHROPIC_API_KEY",
        "default_model": "claude-sonnet-4-6",
        "models": {
            "claude-opus-4-7":        "Most capable — best for complex multi-step debugging (paid)",
            "claude-sonnet-4-6":      "Balanced speed & capability — recommended (paid)",
            "claude-haiku-4-5-20251001": "Fastest & lowest cost (paid)",
        },
    },
    "openai": {
        "env_key": "OPENAI_API_KEY",
        "default_model": "gpt-4o",
        "models": {
            "o3":        "Most capable — best for complex reasoning (paid)",
            "gpt-4o":    "Balanced speed & capability (paid)",
            "gpt-4o-mini": "Fast & cost-effective (paid)",
        },
    },
}


class Config(BaseModel):
    provider: str = "anthropic"
    model: str = "claude-sonnet-4-6"
    max_iterations: int = 20
    verbose: bool = False

    def api_key(self) -> str | None:
        env_key = PROVIDERS.get(self.provider, {}).get("env_key", "")
        return os.getenv(env_key) or None


def load_config() -> Config:
    cfg: dict = {}
    if CONFIG_PATH.exists():
        try:
            cfg = json.loads(CONFIG_PATH.read_text())
        except Exception:
            pass

    if os.getenv("OPSMATE_PROVIDER"):
        cfg["provider"] = os.getenv("OPSMATE_PROVIDER")
    if os.getenv("OPSMATE_MODEL"):
        cfg["model"] = os.getenv("OPSMATE_MODEL")

    return Config(**cfg)


def save_config(config: Config) -> None:
    CONFIG_PATH.parent.mkdir(parents=True, exist_ok=True)
    CONFIG_PATH.write_text(config.model_dump_json(indent=2))
