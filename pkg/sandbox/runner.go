// Package sandbox provides the interface and implementations for running
// tool scripts in isolated environments.
package sandbox

import (
	"context"
	"time"
)

// Language identifies the script runtime.
type Language string

const (
	LangPython Language = "python"
	LangBash   Language = "bash"
	LangGo     Language = "go"
)

// Spec describes a single tool execution.
type Spec struct {
	ToolID       string
	Language     Language
	Entrypoint   string   // path to script inside image
	ScriptContent string  // raw script bytes (written to a volume)
	ParamsJSON   string   // JSON to write to $PARAMS_PATH
	RepoURL      string   // git URL of target repo to clone into $REPO_PATH
	RepoBranch   string
	Image        string   // container image to run
	Dependencies []string // pip/apt packages to pre-install

	TimeoutSeconds int
	CPULimit       string // k8s resource quantity e.g. "500m"
	MemoryLimit    string // k8s resource quantity e.g. "512Mi"
	Namespace      string // k8s namespace
}

// Result is the output of a completed sandbox run.
type Result struct {
	ExitCode  int
	Stdout    string // the unified diff
	Stderr    string // capped, for debugging only
	PodName   string
	StartedAt time.Time
	FinishedAt time.Time
}

// Runner executes a tool script in an isolated sandbox.
type Runner interface {
	Run(ctx context.Context, spec Spec) (Result, error)
}
