package runner

import (
	"fmt"
	"strings"

	"github.com/vishu42/tflive/internal/domain"
)

// CommandError identifies which Terraform command failed. Callers can recover
// it with errors.As to branch on the failing command instead of parsing the
// error message.
type CommandError struct {
	Command domain.TerraformCommandType
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

// newGitCommandError builds a GitCommandError with every literal form of
// credential scrubbed from output.
//
// Git itself strips userinfo from URLs before printing them, so this is not the
// only thing standing between a token and a log. It is the last one: Output is
// persisted as a template registration's error summary and rendered in the UI,
// so scrubbing here keeps that safe no matter which git flags a later change
// introduces.
func newGitCommandError(command GitCommand, output string, credential GitCredential, err error) *GitCommandError {
	for _, secret := range credential.secrets() {
		output = strings.ReplaceAll(output, secret, "******")
	}
	return &GitCommandError{Command: command, Output: strings.TrimSpace(output), Err: err}
}
