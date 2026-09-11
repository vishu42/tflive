package runner

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// A credential that renders itself is a credential in a log file. Both verbs
// matter: %v reaches String, %#v reaches GoString.
func TestGitCredentialRedactsItself(t *testing.T) {
	t.Parallel()

	credential := NewGitCredential("ghs_supersecrettoken")

	if got := fmt.Sprintf("%v", credential); got != "[REDACTED]" {
		t.Fatalf("%%v = %q, want [REDACTED]", got)
	}
	if got := fmt.Sprintf("%#v", credential); got != "[REDACTED]" {
		t.Fatalf("%%#v = %q, want [REDACTED]", got)
	}
	holder := struct{ Credential GitCredential }{Credential: credential}
	if got := fmt.Sprintf("%+v", holder); strings.Contains(got, "ghs_supersecrettoken") {
		t.Fatalf("%%+v = %q, want no token", got)
	}
}

func TestGitCredentialEmpty(t *testing.T) {
	t.Parallel()

	if !NewGitCredential("").Empty() {
		t.Fatal("empty token should produce an empty credential")
	}
	if NewGitCredential("ghs_token").Empty() {
		t.Fatal("non-empty token should not produce an empty credential")
	}
}

// The header is the whole mechanism: it must be scoped to github.com over
// HTTPS, must clear any inherited extraheader, and must be named Authorization
// so git's own trace redactor recognises it.
func TestGitEnvironmentCarriesScopedAuthorizationHeader(t *testing.T) {
	t.Parallel()

	got := gitEnvironment(NewGitCredential("ghs_token"))

	want := []string{
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=true",
		"GIT_TRACE=",
		"GIT_TRACE_PACKET=",
		"GIT_TRACE_CURL=",
		"GIT_TRACE_REDACT=1",
		"GIT_CONFIG_COUNT=2",
		"GIT_CONFIG_KEY_0=http.extraheader",
		"GIT_CONFIG_VALUE_0=",
		"GIT_CONFIG_KEY_1=http.https://github.com/.extraheader",
		// base64("x-access-token:ghs_token")
		"GIT_CONFIG_VALUE_1=Authorization: Basic eC1hY2Nlc3MtdG9rZW46Z2hzX3Rva2Vu",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("env = %#v, want %#v", got, want)
	}
}

// Without a credential the git config keys must be absent entirely: an empty
// Authorization header is not the same as no header, and public clones must
// keep working untouched.
func TestGitEnvironmentWithoutCredentialSetsNoConfig(t *testing.T) {
	t.Parallel()

	got := gitEnvironment(GitCredential{})

	want := []string{
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=true",
		"GIT_TRACE=",
		"GIT_TRACE_PACKET=",
		"GIT_TRACE_CURL=",
		"GIT_TRACE_REDACT=1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("env = %#v, want %#v", got, want)
	}
}

// Output becomes a registration's error summary in Postgres and is rendered in
// the UI. Both the raw token and the wire form must be gone by then.
func TestNewGitCommandErrorScrubsCredential(t *testing.T) {
	t.Parallel()

	credential := NewGitCredential("ghs_token")
	output := "fatal: ghs_token rejected (eC1hY2Nlc3MtdG9rZW46Z2hzX3Rva2Vu)\n"

	err := newGitCommandError(GitCommandFetch, output, credential, errors.New("exit status 128"))

	if strings.Contains(err.Output, "ghs_token") {
		t.Fatalf("Output = %q, want raw token scrubbed", err.Output)
	}
	if strings.Contains(err.Output, "eC1hY2Nlc3MtdG9rZW46Z2hzX3Rva2Vu") {
		t.Fatalf("Output = %q, want basic-auth blob scrubbed", err.Output)
	}
	if !strings.Contains(err.Output, "******") {
		t.Fatalf("Output = %q, want redaction marker", err.Output)
	}
	if err.Command != GitCommandFetch {
		t.Fatalf("Command = %q, want %q", err.Command, GitCommandFetch)
	}
}
