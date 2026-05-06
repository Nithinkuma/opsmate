// Package tool provides manifest parsing, hash verification, and the in-memory
// tool registry index.
package tool

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/goccy/go-yaml"
)

// Manifest is the parsed, validated representation of a tool's manifest.yaml.
type Manifest struct {
	ID            string        `yaml:"id"`
	Verb          string        `yaml:"verb"`
	SchemaVersion int           `yaml:"schema_version"`
	Repo          RepoConfig    `yaml:"repo"`
	Runtime       RuntimeConfig `yaml:"runtime"`
	Preconditions []string      `yaml:"preconditions"`
	Postconditions []string     `yaml:"postconditions"`
	Validation    ValidationCfg `yaml:"validation"`
	Provenance    Provenance    `yaml:"provenance"`
	Hash          HashConfig    `yaml:"hash"`

	// ScriptContent is the actual script file content, loaded alongside the manifest.
	ScriptContent string `yaml:"-"`
	// Dir is the directory containing this tool's files.
	Dir string `yaml:"-"`
}

type RepoConfig struct {
	Pattern       string `yaml:"pattern"`
	DefaultBranch string `yaml:"default_branch"`
}

type RuntimeConfig struct {
	Language     string   `yaml:"language"`
	Entrypoint   string   `yaml:"entrypoint"`
	Interpreter  string   `yaml:"interpreter"`
	Dependencies []string `yaml:"dependencies"`
}

type ValidationCfg struct {
	DryRunCommand string `yaml:"dry_run_command"`
	GoldenTests   string `yaml:"golden_tests"`
}

type Provenance struct {
	GeneratedBy    string    `yaml:"generated_by"`
	GeneratedAt    time.Time `yaml:"generated_at"`
	ApprovedBy     string    `yaml:"approved_by"`
	ApprovalPR     string    `yaml:"approval_pr"`
	LLMModel       string    `yaml:"llm_model"`
	SourceExamples []struct {
		BitbucketPR string `yaml:"bitbucket_pr"`
	} `yaml:"source_examples"`
}

type HashConfig struct {
	Manifest string `yaml:"manifest"`
	Script   string `yaml:"script"`
}

// ParseManifest reads and strictly parses a manifest.yaml file.
func ParseManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("tool: read manifest %s: %w", path, err)
	}

	var m Manifest
	if err := yaml.UnmarshalWithOptions(data, &m, yaml.Strict()); err != nil {
		return nil, fmt.Errorf("tool: parse manifest %s: %w", path, err)
	}

	m.Dir = filepath.Dir(path)

	// Load the script alongside the manifest.
	if m.Runtime.Entrypoint != "" {
		scriptPath := filepath.Join(m.Dir, m.Runtime.Entrypoint)
		scriptData, err := os.ReadFile(scriptPath)
		if err != nil {
			return nil, fmt.Errorf("tool: read script %s: %w", scriptPath, err)
		}
		m.ScriptContent = string(scriptData)
	}

	if err := m.validate(); err != nil {
		return nil, fmt.Errorf("tool: invalid manifest %s: %w", path, err)
	}
	return &m, nil
}

func (m *Manifest) validate() error {
	if m.ID == "" {
		return fmt.Errorf("id is required")
	}
	if m.Verb == "" {
		return fmt.Errorf("verb is required")
	}
	if m.Repo.Pattern == "" {
		return fmt.Errorf("repo.pattern is required")
	}
	if m.Runtime.Language == "" {
		return fmt.Errorf("runtime.language is required")
	}
	switch m.Runtime.Language {
	case "python", "bash", "go":
	default:
		return fmt.Errorf("runtime.language must be python|bash|go, got %q", m.Runtime.Language)
	}
	return nil
}

// VerifyHashes checks that the stored hashes match the actual file contents.
// Returns an error if either hash is wrong — hard failure per spec §4.
func (m *Manifest) VerifyHashes() error {
	if m.Hash.Script != "" && m.ScriptContent != "" {
		got := "sha256:" + hexHash([]byte(m.ScriptContent))
		if m.Hash.Script != got {
			return fmt.Errorf("tool: script hash mismatch for %s: stored=%s computed=%s",
				m.ID, m.Hash.Script, got)
		}
	}
	return nil
}

// ComputeHashes fills m.Hash with freshly computed values. Used when
// creating a new manifest entry (not for verification).
func (m *Manifest) ComputeHashes() {
	if m.ScriptContent != "" {
		m.Hash.Script = "sha256:" + hexHash([]byte(m.ScriptContent))
	}
}

// ParseManifestFromString parses a manifest from raw YAML + script content
// without requiring files on disk. Used by the generator to validate a
// generated tool before pushing it to the tools-registry.
func ParseManifestFromString(yamlContent, scriptContent, language string) (*Manifest, error) {
	var m Manifest
	if err := yaml.UnmarshalWithOptions([]byte(yamlContent), &m, yaml.Strict()); err != nil {
		return nil, fmt.Errorf("tool: parse manifest YAML: %w", err)
	}
	m.ScriptContent = scriptContent
	// The explicit language parameter overrides whatever the LLM put in the YAML,
	// since emit_tool carries it as a top-level field.
	if language != "" {
		m.Runtime.Language = language
	}
	if err := m.validate(); err != nil {
		return nil, fmt.Errorf("tool: invalid manifest: %w", err)
	}
	return &m, nil
}

func hexHash(data []byte) string {
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h)
}
