package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nithinkuma/opsmate/pkg/config"
	"github.com/stretchr/testify/require"
)

const minimalConfig = `
llm:
  primary:
    provider: anthropic
    model: claude-haiku-4-5-20251001
    api_key: test-key
  fast:
    provider: anthropic
    model: claude-haiku-4-5-20251001
    api_key: test-key

mcp:
  atlassian:
    url: http://localhost:9001
    auth: test-token
  bitbucket:
    url: http://localhost:9002
    auth: test-token

postgres:
  dsn: ""

sandbox:
  kind: local
  namespace: agent-sandbox

tools_registry:
  seed_path: ./tools-registry-seed
  poll_interval: 60s

agent:
  generator_max_steps: 15
  generator_max_parallel_tools: 4

observability:
  log_level: info
`

func TestLoad_ValidConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte(minimalConfig), 0644))

	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)
	require.Equal(t, "anthropic", cfg.LLM.Primary.Provider)
	require.Equal(t, "claude-haiku-4-5-20251001", cfg.LLM.Primary.Model)
	require.Equal(t, "local", cfg.Sandbox.Kind)
	require.Equal(t, 15, cfg.Agent.GeneratorMaxSteps)
}

func TestLoad_EnvOverride(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte(minimalConfig), 0644))

	t.Setenv("AGENT_LLM_PRIMARY_MODEL", "claude-opus-4-7")

	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)
	require.Equal(t, "claude-opus-4-7", cfg.LLM.Primary.Model)
}

func TestLoad_MissingFile(t *testing.T) {
	_, err := config.Load("/nonexistent/path/config.yaml")
	require.Error(t, err)
}
