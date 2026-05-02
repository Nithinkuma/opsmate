package llm

import "fmt"

// Config holds the configuration for one LLM slot (primary or fast).
type Config struct {
	Provider string `mapstructure:"provider"`
	Model    string `mapstructure:"model"`
	APIKey   string `mapstructure:"api_key"` // also read from env by viper
}

// New creates a Client from a Config.
func New(cfg Config) (Client, error) {
	switch cfg.Provider {
	case "anthropic":
		return NewAnthropicClient(cfg.APIKey, cfg.Model), nil
	case "openai":
		return NewOpenAIClient(cfg.APIKey, cfg.Model), nil
	case "ollama":
		base := cfg.APIKey // reuse field as base URL for Ollama
		if base == "" {
			base = "http://localhost:11434"
		}
		return NewOllamaClient(base, cfg.Model), nil
	default:
		return nil, fmt.Errorf("llm: unknown provider %q", cfg.Provider)
	}
}
