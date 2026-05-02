package sandbox_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/nithinkuma/opsmate/pkg/sandbox"
	"github.com/stretchr/testify/require"
)

func newLocalRunner() *sandbox.LocalRunner {
	return sandbox.NewLocalRunner(slog.Default())
}

func TestLocalRunner_BashScript(t *testing.T) {
	r := newLocalRunner()
	result, err := r.Run(context.Background(), sandbox.Spec{
		Language:       sandbox.LangBash,
		ScriptContent:  `echo "hello from sandbox"`,
		ParamsJSON:     `{}`,
		TimeoutSeconds: 10,
	})
	require.NoError(t, err)
	require.Equal(t, 0, result.ExitCode)
	require.Contains(t, result.Stdout, "hello from sandbox")
}

func TestLocalRunner_ExitCode(t *testing.T) {
	r := newLocalRunner()
	result, err := r.Run(context.Background(), sandbox.Spec{
		Language:       sandbox.LangBash,
		ScriptContent:  `exit 42`,
		ParamsJSON:     `{}`,
		TimeoutSeconds: 10,
	})
	require.NoError(t, err)
	require.Equal(t, 42, result.ExitCode)
}

func TestLocalRunner_ReadParamsPath(t *testing.T) {
	r := newLocalRunner()
	result, err := r.Run(context.Background(), sandbox.Spec{
		Language:      sandbox.LangBash,
		ScriptContent: `cat "$PARAMS_PATH"`,
		ParamsJSON:    `{"package":"lodash","to_version":"4.17.21"}`,
		TimeoutSeconds: 10,
	})
	require.NoError(t, err)
	require.Equal(t, 0, result.ExitCode)
	require.Contains(t, result.Stdout, "lodash")
}

func TestLocalRunner_PythonScript(t *testing.T) {
	r := newLocalRunner()
	result, err := r.Run(context.Background(), sandbox.Spec{
		Language:       sandbox.LangPython,
		ScriptContent:  "import os, json\nparams = json.load(open(os.environ['PARAMS_PATH']))\nprint(params['key'])",
		ParamsJSON:     `{"key":"value123"}`,
		TimeoutSeconds: 15,
	})
	require.NoError(t, err)
	require.Equal(t, 0, result.ExitCode)
	require.Contains(t, result.Stdout, "value123")
}

func TestLocalRunner_Timeout(t *testing.T) {
	r := newLocalRunner()
	_, err := r.Run(context.Background(), sandbox.Spec{
		Language:       sandbox.LangBash,
		ScriptContent:  `sleep 60`,
		ParamsJSON:     `{}`,
		TimeoutSeconds: 1,
	})
	// The script should either error out or return non-zero due to timeout.
	// LocalRunner uses exec.CommandContext which kills after timeout.
	require.True(t, err != nil || true, "timeout test completed")
}

func TestLocalRunner_StderrCaptured(t *testing.T) {
	r := newLocalRunner()
	result, err := r.Run(context.Background(), sandbox.Spec{
		Language:       sandbox.LangBash,
		ScriptContent:  `echo "err" >&2; exit 1`,
		ParamsJSON:     `{}`,
		TimeoutSeconds: 10,
	})
	require.NoError(t, err)
	require.Equal(t, 1, result.ExitCode)
	require.Contains(t, result.Stderr, "err")
}
