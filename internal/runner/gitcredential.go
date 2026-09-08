package runner

import "encoding/base64"

// GitCredential carries a short-lived token authenticating one git operation
// against a remote.
//
// String and GoString both render "[REDACTED]", so a credential cannot reach a
// log through %v, %+v, or %#v on any struct that holds one. This mirrors
// config.Secret, which guards the same hazard for configuration values.
type GitCredential struct {
	token string
}

// NewGitCredential returns a credential carrying token. An empty token yields
// the zero credential, which authenticates nothing.
func NewGitCredential(token string) GitCredential {
	return GitCredential{token: token}
}

// Empty reports whether this credential would authenticate anything.
func (credential GitCredential) Empty() bool { return credential.token == "" }

func (GitCredential) String() string { return "[REDACTED]" }

func (GitCredential) GoString() string { return "[REDACTED]" }

// basicAuth renders the credential as it travels on the wire. GitHub ignores
// the username; "x-access-token" is its documented convention for signalling
// that the password field holds an installation token.
func (credential GitCredential) basicAuth() string {
	return base64.StdEncoding.EncodeToString([]byte("x-access-token:" + credential.token))
}

// secrets returns every literal form this credential can take in command
// output, so all of them can be scrubbed before the output is persisted.
func (credential GitCredential) secrets() []string {
	if credential.Empty() {
		return nil
	}
	return []string{credential.token, credential.basicAuth()}
}

// gitEnvironment returns the environment for one git invocation.
//
// The credential travels as an HTTP header configured through GIT_CONFIG_*
// rather than through the clone URL or an argv flag. Git applies these at
// "command line" scope, so nothing is written to the repository's .git/config
// -- which matters because Terraform later executes from that same directory --
// and nothing appears in argv, where any process sharing the host could read it.
//
// Index 0 clears any http.extraheader inherited from a system or user gitconfig.
// extraheader is multi-valued and accumulates, and an empty value resets the
// list, so this stops a polluted HOME from adding a second header to the request.
//
// The trace variables are neutralised rather than trusted. The executor appends
// to os.Environ(), and GIT_TRACE_CURL=1 with GIT_TRACE_REDACT=0 prints the
// Authorization header in full -- into output that is persisted as a template
// registration's error summary and rendered in the UI. GIT_TERMINAL_PROMPT and
// GIT_ASKPASS are pinned for a different reason: a rejected credential must fail
// immediately instead of blocking on a username prompt until the activity's
// Temporal timeout expires.
func gitEnvironment(credential GitCredential) []string {
	environment := []string{
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=true",
		"GIT_TRACE=",
		"GIT_TRACE_PACKET=",
		"GIT_TRACE_CURL=",
		"GIT_TRACE_REDACT=1",
	}
	if credential.Empty() {
		return environment
	}
	return append(environment,
		"GIT_CONFIG_COUNT=2",
		"GIT_CONFIG_KEY_0=http.extraheader",
		"GIT_CONFIG_VALUE_0=",
		"GIT_CONFIG_KEY_1=http.https://github.com/.extraheader",
		"GIT_CONFIG_VALUE_1=Authorization: Basic "+credential.basicAuth(),
	)
}
