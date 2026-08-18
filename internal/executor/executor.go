package executor

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"time"
)

// Result holds the outcome of a subprocess execution.
type Result struct {
	ExitCode int
	Stdout   string
	Stderr   string
	Duration time.Duration
	TimedOut bool
	Error    error
}

// Options configures job execution.
type Options struct {
	Command string
	WorkDir string
	Env     map[string]string
	Timeout time.Duration
}

// Run executes a command in a shell subprocess.
func Run(ctx context.Context, opts Options) Result {
	start := time.Now()

	if opts.WorkDir == "" {
		opts.WorkDir, _ = os.Getwd()
	}

	shell, flag := shellCommand()
	cmd := exec.CommandContext(ctx, shell, flag, opts.Command)
	cmd.Dir = opts.WorkDir
	cmd.Env = buildEnv(opts.Env)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	duration := time.Since(start)

	result := Result{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Duration: duration,
	}

	if ctx.Err() == context.DeadlineExceeded {
		result.TimedOut = true
		result.ExitCode = 1
		result.Error = fmt.Errorf("job timed out after %s", opts.Timeout)
		return result
	}

	if err != nil {
		result.Error = err
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else {
			result.ExitCode = 1
		}
		return result
	}

	result.ExitCode = 0
	return result
}

func shellCommand() (string, string) {
	if runtime.GOOS == "windows" {
		return "cmd", "/C"
	}
	return "sh", "-c"
}

func buildEnv(overrides map[string]string) []string {
	base := os.Environ()
	if len(overrides) == 0 {
		return base
	}

	envMap := make(map[string]string)
	for _, e := range base {
		if idx := indexByte(e, '='); idx > 0 {
			envMap[e[:idx]] = e[idx+1:]
		}
	}
	for k, v := range overrides {
		envMap[k] = v
	}

	result := make([]string, 0, len(envMap))
	for k, v := range envMap {
		result = append(result, k+"="+v)
	}
	return result
}

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}
