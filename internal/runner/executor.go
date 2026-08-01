package runner

import (
	"context"
	"io"
	"os"
	"os/exec"
)

// CommandExecutor is the subprocess boundary shared by LocalProcessRunner and
// LocalGitRunner.
//
// Implementations receive the fully resolved working directory, output streams,
// executable name, and CLI arguments. The production executor starts a real
// process with os/exec, while tests can provide a recorder or fake executor to
// verify command construction without invoking a subprocess.
type CommandExecutor interface {
	Run(ctx context.Context, dir string, env []string, stdout io.Writer, stderr io.Writer, name string, args ...string) error
}

type osExecCommandExecutor struct{}

// Run starts one subprocess in dir and connects its output streams.
//
// The context is passed to exec.CommandContext so cancellation or deadline
// expiry can terminate the process. The command name and args are
// intentionally supplied by the caller so this adapter stays generic.
func (osExecCommandExecutor) Run(ctx context.Context, dir string, env []string, stdout io.Writer, stderr io.Writer, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	return cmd.Run()
}
