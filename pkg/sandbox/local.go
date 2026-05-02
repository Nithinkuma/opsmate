package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// LocalRunner executes scripts directly on the host. Dev-only.
// Every invocation logs a prominent warning per spec §2.4.
type LocalRunner struct {
	log *slog.Logger
}

// NewLocalRunner creates a dev-only local sandbox runner.
func NewLocalRunner(log *slog.Logger) *LocalRunner {
	return &LocalRunner{log: log}
}

func (r *LocalRunner) Run(ctx context.Context, spec Spec) (Result, error) {
	r.log.Warn("WARN: local sandbox is dev-only — never use in production")

	tmp, err := os.MkdirTemp("", "opsmate-sandbox-*")
	if err != nil {
		return Result{}, fmt.Errorf("local sandbox: mktemp: %w", err)
	}
	defer os.RemoveAll(tmp)

	// Write params JSON.
	paramsPath := filepath.Join(tmp, "params.json")
	if err := os.WriteFile(paramsPath, []byte(spec.ParamsJSON), 0600); err != nil {
		return Result{}, fmt.Errorf("local sandbox: write params: %w", err)
	}

	// Write the script.
	scriptPath := filepath.Join(tmp, "script")
	if err := os.WriteFile(scriptPath, []byte(spec.ScriptContent), 0700); err != nil {
		return Result{}, fmt.Errorf("local sandbox: write script: %w", err)
	}

	// Clone the repo.
	repoPath := filepath.Join(tmp, "repo")
	if spec.RepoURL != "" {
		cloneArgs := []string{"clone", "--depth=1"}
		if spec.RepoBranch != "" {
			cloneArgs = append(cloneArgs, "--branch", spec.RepoBranch)
		}
		cloneArgs = append(cloneArgs, spec.RepoURL, repoPath)
		cloneCmd := exec.CommandContext(ctx, "git", cloneArgs...)
		if out, err := cloneCmd.CombinedOutput(); err != nil {
			return Result{}, fmt.Errorf("local sandbox: git clone: %w\n%s", err, out)
		}
	} else {
		_ = os.MkdirAll(repoPath, 0755)
	}

	timeout := time.Duration(spec.TimeoutSeconds) * time.Second
	if timeout == 0 {
		timeout = 300 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	interpreter := interpreterFor(spec.Language)
	cmd := exec.CommandContext(runCtx, interpreter, scriptPath)
	cmd.Env = append(os.Environ(),
		"PARAMS_PATH="+paramsPath,
		"REPO_PATH="+repoPath,
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	runErr := cmd.Run()
	end := time.Now()

	exitCode := 0
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}

	stderrStr := stderr.String()
	if len(stderrStr) > 2048 {
		stderrStr = stderrStr[len(stderrStr)-2048:]
	}

	return Result{
		ExitCode:   exitCode,
		Stdout:     stdout.String(),
		Stderr:     stderrStr,
		StartedAt:  start,
		FinishedAt: end,
	}, nil
}

func interpreterFor(lang Language) string {
	switch lang {
	case LangPython:
		return "python3"
	case LangBash:
		return "bash"
	case LangGo:
		return "go"
	default:
		return "bash"
	}
}
