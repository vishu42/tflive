package runner

import (
	"fmt"

	"github.com/vishu42/tflive/internal/traits"
)

// CommandError identifies which Terraform command failed. Callers can recover
// it with errors.As to branch on the failing command instead of parsing the
// error message.
type CommandError struct {
	Command traits.TerraformCommandType
	Err     error
}

func (e *CommandError) Error() string {
	return fmt.Sprintf("%s: %s", e.Command, e.Err)
}

func (e *CommandError) Unwrap() error {
	return e.Err
}

// GitCommand identifies which git operation LocalGitRunner ran.
type GitCommand string

const (
	GitCommandClone       GitCommand = "clone"
	GitCommandInit        GitCommand = "init"
	GitCommandRemoteAdd   GitCommand = "remote_add"
	GitCommandFetch       GitCommand = "fetch"
	GitCommandCheckout    GitCommand = "checkout"
	GitCommandResolveHead GitCommand = "resolve_head"
)

// GitCommandError identifies which git operation failed. Callers can recover
// it with errors.As to branch on the failing command instead of parsing the
// error message. Output carries the command's combined stdout/stderr for
// diagnostics, since git failures are usually only explained there.
type GitCommandError struct {
	Command GitCommand
	Output  string
	Err     error
}

func (e *GitCommandError) Error() string {
	if e.Output == "" {
		return fmt.Sprintf("%s: %s", e.Command, e.Err)
	}
	return fmt.Sprintf("%s: %s: %s", e.Command, e.Err, e.Output)
}

func (e *GitCommandError) Unwrap() error {
	return e.Err
}
