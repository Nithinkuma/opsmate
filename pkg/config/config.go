// Package config loads and exposes the agent configuration.
package config

import (
	"fmt"
	"strings"

	"github.com/nithinkuma/opsmate/pkg/llm"
	"github.com/spf13/viper"
)

// Config is the top-level agent configuration.
type Config struct {
	LLM           LLMConfig           `mapstructure:"llm"`
	MCP           MCPConfig           `mapstructure:"mcp"`
	Postgres      PostgresConfig      `mapstructure:"postgres"`
	Sandbox       SandboxConfig       `mapstructure:"sandbox"`
	ToolsRegistry ToolsRegistryConfig `mapstructure:"tools_registry"`
	Agent         AgentConfig         `mapstructure:"agent"`
	Observability ObservabilityConfig `mapstructure:"observability"`
}

type LLMConfig struct {
	Primary llm.Config `mapstructure:"primary"`
	Fast    llm.Config `mapstructure:"fast"`
}

type MCPEndpoint struct {
	URL  string `mapstructure:"url"`
	Auth string `mapstructure:"auth"`
}

type MCPConfig struct {
	Bitbucket MCPEndpoint `mapstructure:"bitbucket"`
	Atlassian MCPEndpoint `mapstructure:"atlassian"`
}

type PostgresConfig struct {
	DSN string `mapstructure:"dsn"`
}

type RuntimeImages struct {
	Python string `mapstructure:"python"`
	Bash   string `mapstructure:"bash"`
	Go     string `mapstructure:"go"`
}

type SandboxConfig struct {
	Kind           string        `mapstructure:"kind"`
	Namespace      string        `mapstructure:"namespace"`
	RuntimeImages  RuntimeImages `mapstructure:"runtime_images"`
	DefaultTimeout string        `mapstructure:"default_timeout"`
	CPULimit       string        `mapstructure:"cpu_limit"`
	MemoryLimit    string        `mapstructure:"memory_limit"`
}

type ToolsRegistryConfig struct {
	GitURL       string `mapstructure:"git_url"`
	Branch       string `mapstructure:"branch"`
	PollInterval string `mapstructure:"poll_interval"`
	WebhookSecret string `mapstructure:"webhook_secret"`
	SeedPath     string `mapstructure:"seed_path"`
}

type AgentConfig struct {
	GeneratorMaxSteps        int     `mapstructure:"generator_max_steps"`
	GeneratorMaxParallelTools int    `mapstructure:"generator_max_parallel_tools"`
	IntentTemperature        float64 `mapstructure:"intent_temperature"`
	GeneratorTemperature     float64 `mapstructure:"generator_temperature"`
}

type ObservabilityConfig struct {
	OTLPEndpoint string `mapstructure:"otlp_endpoint"`
	LogLevel     string `mapstructure:"log_level"`
}

// Load reads config.yaml from the current directory (or path override),
// then overlays AGENT_* environment variables.
func Load(cfgFile string) (*Config, error) {
	v := viper.New()
	v.SetConfigType("yaml")
	v.SetEnvPrefix("AGENT")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	if cfgFile != "" {
		v.SetConfigFile(cfgFile)
	} else {
		v.SetConfigName("config")
		v.AddConfigPath(".")
		v.AddConfigPath("$HOME/.opsmate")
	}

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("config: read: %w", err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("config: unmarshal: %w", err)
	}
	return &cfg, nil
}
