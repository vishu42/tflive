# Private GitHub Repository Access Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development
> (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let tflive register templates from, and run Terraform against, private GitHub
repositories by authenticating its git operations with a short-lived GitHub App installation
token.

**Architecture:** A GitHub App's ID and RSA private key come from worker config. Inside the
worker activity — never in Temporal workflow input — we sign a short-lived App JWT, resolve
which installation covers `{owner}/{repo}`, and exchange the JWT for a repo-scoped read-only
installation token. That token reaches `git` only through `GIT_CONFIG_*` environment variables
carrying an `http.<url>.extraheader`, so it never appears in argv, in the clone URL, or in the
workspace's `.git/config` — the directory Terraform subsequently executes from. With no App
configured, every code path behaves exactly as it does today.

**Tech Stack:** Go 1.25, `net/http` + `encoding/json` for the GitHub API,
`github.com/lestrrat-go/jwx/v3` for RS256 JWT signing (already a direct dependency), Temporal
activities, `git` CLI via the existing `runner.CommandExecutor` boundary.

**Spec:** https://github.com/vishu42/tflive/issues/239 (sub-issue of epic #156). Read it before
starting — it carries the rationale for decisions this plan only states.

## TL;DR

Eight tasks, each ending in a green test suite and a commit.

| # | Task | Deliverable |
|---|---|---|
| 1 | Drop `remote add` | `CheckoutCommit` runs 3 git invocations; repo URL never enters `.git/config` |
| 2 | `GitCredential` + env | Self-redacting token type, `GIT_CONFIG_*` builder, error-output scrubbing |
| 3 | Thread through `GitRunner` | Interface + all call sites; empty credential everywhere, behaviour unchanged |
| 4 | `internal/githubapp` client | Key parsing, App JWT, installation lookup, token minting |
| 5 | `TokenSource` | Per-repo cache with a 5-minute expiry margin |
| 6 | Worker config | `GITHUB_APP_ID` / `GITHUB_APP_PRIVATE_KEY`, validated at boot |
| 7 | Activity wiring | Both fetch paths authenticated; "App not installed" classified non-retryable |
| 8 | Worker + docs | Construct the source, `.env.example`, compose, architecture doc |

**Ordering is deliberate.** Tasks 1-3 are reversible refactors that leave behaviour unchanged, so
the build stays green throughout and the security-sensitive work in 4-8 lands on a clean base.

**The one rule everything else serves:** the token reaches git *only* through environment
variables. Never argv, never the clone URL, never `.git/config` — because Terraform executes from
that directory afterwards.

**Expect exactly one intentional build break:** after Task 7, `cmd/worker` will not compile until
Task 8 updates the constructor call. Anywhere else, a broken build means something went wrong.

**Do not skip manual verification step 6**, the leak audit. It is the only check that proves the
security claim — no automated test can reach a real workspace on a real clone.

## Global Constraints

- **No new Go dependencies.** `jwx/v3` covers JWT signing; everything else is stdlib. Do not add
  `google/go-github` or `bradleyfalzon/ghinstallation`.
- **Go 1.25.0**, toolchain go1.25.14 (`go.mod`).
- **git ≥ 2.31** required at runtime for `GIT_CONFIG_COUNT`. `Dockerfile.worker` installs git via
  `apk` on Alpine (2.47+). Do not add a version check; note it in docs only.
- **tflive is pre-production.** No users, disposable state. Never write migrations for backward
  compatibility, never preserve a deprecated signature "just in case". Deleting
  `GitCommandRemoteAdd` is correct, not a breaking change.
- **The token must never reach:** argv, a clone URL, `.git/config`, Temporal workflow input or
  history, an API response, or `template_registrations.error_summary`.
- **Naming the HTTP header `Authorization` is load-bearing** — git's trace redactor keys off that
  exact name. Do not rename it.
- Follow existing house style: table-driven-ish tests with `t.Parallel()`, `reflect.DeepEqual`
  against a `want` literal, errors wrapped with `%w`, doc comments that explain *why*.

---

## File Structure

**Created:**

| File | Responsibility |
|---|---|
| `internal/runner/gitcredential.go` | `GitCredential` value type (self-redacting) + `gitEnvironment()` builder |
| `internal/runner/gitcredential_test.go` | Redaction, env construction |
| `internal/githubapp/key.go` | `ParsePrivateKey` — PEM / base64-PEM → `*rsa.PrivateKey` |
| `internal/githubapp/key_test.go` | PKCS#1, PKCS#8, base64, malformed |
| `internal/githubapp/errors.go` | `ErrAppNotInstalled` sentinel |
| `internal/githubapp/client.go` | App JWT, `InstallationForRepo`, `MintToken` |
| `internal/githubapp/client_test.go` | `httptest` fixtures |
| `internal/githubapp/tokensource.go` | `TokenSource` — resolve + mint + cache |
| `internal/githubapp/tokensource_test.go` | Cache hit/miss, expiry margin, nil source |

**Modified:**

| File | Change |
|---|---|
| `internal/runner/git.go` | Drop `remote add`; thread `GitCredential`; pass env |
| `internal/runner/errors.go` | `newGitCommandError` scrubbing constructor; delete `GitCommandRemoteAdd` |
| `internal/runner/git_test.go` | Update expectations |
| `internal/activities/template_sync.go` | Token source, `gitHubRepoURL`, owner/repo validation, error classification |
| `internal/activities/template_run.go` | Token source on `FetchSource` |
| `internal/activities/*_test.go` | Update fakes for new interface |
| `internal/config/config.go` | `GitHubAppConfig` on `WorkerConfig` |
| `internal/config/config_test.go` | Loader cases |
| `cmd/worker/main.go` | Construct and inject `TokenSource` |
| `.env.example`, `docker-compose.app.yaml`, `docs/architecture.md` | Config + docs |

---

## Task 1: Drop the `remote add` step from `CheckoutCommit`

Pure refactor, no auth. `git fetch` accepts a URL positionally, so the intermediate remote is
unnecessary — and removing it means the repo URL is never written into the run workspace's
`.git/config`, which is where Terraform later executes.

**Files:**
- Modify: `internal/runner/git.go:46-63`
- Modify: `internal/runner/errors.go` (delete `GitCommandRemoteAdd`)
- Test: `internal/runner/git_test.go:37-88`

**Interfaces:**
- Consumes: nothing (first task).
- Produces: `CheckoutCommit` performs exactly three git invocations —
  `init --quiet <dest>`, `-C <dest> fetch --depth 1 <repoURL> <commitSHA>`,
  `-C <dest> checkout --quiet FETCH_HEAD`.

- [ ] **Step 1: Update the failing tests**

In `internal/runner/git_test.go`, replace the `want` in `TestLocalGitRunnerChecksOutExactCommit`:

```go
	want := []recordedCommand{
		{name: "git", args: []string{"init", "--quiet", "/tmp/repo"}},
		{name: "git", args: []string{"-C", "/tmp/repo", "fetch", "--depth", "1", "https://github.com/acme/infra-templates.git", "a1b2c3d"}},
		{name: "git", args: []string{"-C", "/tmp/repo", "checkout", "--quiet", "FETCH_HEAD"}},
	}
```

And in `TestLocalGitRunnerReportsWhichCheckoutStepFailed`, the fetch is now the second command:

```go
	executor := &recordingCommandExecutor{
		stdout: "fatal: could not read Username\n",
		// init succeeds; the fetch is what fails.
		errs: []error{nil, commandErr},
	}
```

```go
	// It stops at the failing step rather than running checkout on an empty repo.
	if len(executor.commands) != 2 {
		t.Fatalf("commands = %d, want 2", len(executor.commands))
	}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/runner/ -run TestLocalGitRunner -v`
Expected: FAIL — `TestLocalGitRunnerChecksOutExactCommit` reports 4 recorded commands including
`remote add`, `TestLocalGitRunnerReportsWhichCheckoutStepFailed` reports 3.

- [ ] **Step 3: Remove the step**

In `internal/runner/git.go`, replace the `steps` slice in `CheckoutCommit`:

```go
	steps := []struct {
		command GitCommand
		args    []string
	}{
		{GitCommandInit, []string{"init", "--quiet", dest}},
		{GitCommandFetch, []string{"-C", dest, "fetch", "--depth", "1", repoURL, commitSHA}},
		{GitCommandCheckout, []string{"-C", dest, "checkout", "--quiet", "FETCH_HEAD"}},
	}
```

Extend the existing doc comment above `CheckoutCommit` with a third paragraph:

```go
// The URL is passed to the fetch positionally rather than registered as a
// remote. Nothing reads remote.origin.url from this clone, and keeping it out
// of .git/config keeps the run workspace free of the repository URL -- which
// matters once that URL, or a credential alongside it, would otherwise persist
// in the directory Terraform executes from.
```

In `internal/runner/errors.go`, delete the `GitCommandRemoteAdd` constant.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/runner/ -v && go build ./...`
Expected: PASS, build clean (nothing else referenced `GitCommandRemoteAdd`).

- [ ] **Step 5: Commit**

```bash
git add internal/runner/git.go internal/runner/errors.go internal/runner/git_test.go
git commit -m "refactor: fetch the run commit by URL instead of registering a remote"
```

---

## Task 2: `GitCredential` type, git environment, and output scrubbing

The self-redacting credential type and the environment that carries it. Nothing consumes this
yet — Task 3 wires it in.

**Files:**
- Create: `internal/runner/gitcredential.go`
- Create: `internal/runner/gitcredential_test.go`
- Modify: `internal/runner/errors.go`

**Interfaces:**
- Consumes: nothing from Task 1 beyond a compiling package.
- Produces:
  - `runner.GitCredential` struct; `runner.NewGitCredential(token string) GitCredential`;
    methods `Empty() bool`, `String() string`, `GoString() string`; unexported
    `basicAuth() string` and `secrets() []string`.
  - unexported `gitEnvironment(credential GitCredential) []string`.
  - unexported `newGitCommandError(command GitCommand, output string, credential GitCredential, err error) *GitCommandError`.

- [ ] **Step 1: Write the failing tests**

Create `internal/runner/gitcredential_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/runner/ -run 'GitCredential|GitEnvironment|GitCommandError' -v`
Expected: FAIL to compile — `undefined: NewGitCredential`, `undefined: gitEnvironment`,
`undefined: newGitCommandError`.

- [ ] **Step 3: Write the implementation**

Create `internal/runner/gitcredential.go`:

```go
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
```

In `internal/runner/errors.go`, add `"strings"` to the imports and append:

```go
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/runner/ -v`
Expected: PASS (all tests, including Task 1's).

- [ ] **Step 5: Commit**

```bash
git add internal/runner/gitcredential.go internal/runner/gitcredential_test.go internal/runner/errors.go
git commit -m "feat: add a self-redacting git credential and its environment"
```

---

## Task 3: Thread `GitCredential` through the git runner and call sites

Interface change plus mechanical call-site updates. Every caller passes the zero credential, so
behaviour is unchanged except that all git invocations now carry the prompt and trace guards.

**Files:**
- Modify: `internal/runner/git.go`
- Modify: `internal/runner/git_test.go`
- Modify: `internal/activities/template_sync.go:82`
- Modify: `internal/activities/template_run.go:167,170`
- Modify: `internal/activities/template_sync_test.go:306,320`
- Modify: `internal/activities/template_run_test.go:415,425`

**Interfaces:**
- Consumes: `GitCredential`, `gitEnvironment`, `newGitCommandError` from Task 2.
- Produces:
  ```go
  type GitRunner interface {
      Clone(ctx context.Context, repoURL string, ref string, dest string, credential GitCredential) error
      CheckoutCommit(ctx context.Context, repoURL string, commitSHA string, dest string, credential GitCredential) error
      ResolveHead(ctx context.Context, repoPath string) (string, error)
  }
  ```

- [ ] **Step 1: Write the failing test**

Append to `internal/runner/git_test.go`:

```go
// The credential reaches git only through the environment. Anything in argv is
// visible to every process on the host, and anything in the URL is written into
// the workspace .git/config that Terraform then runs from.
func TestLocalGitRunnerPassesCredentialOnlyThroughEnvironment(t *testing.T) {
	t.Parallel()

	executor := &recordingCommandExecutor{}
	runner := NewLocalGitRunnerWithExecutor(executor)
	credential := NewGitCredential("ghs_supersecrettoken")

	err := runner.CheckoutCommit(context.Background(), "https://github.com/acme/private.git", "a1b2c3d", "/tmp/repo", credential)
	if err != nil {
		t.Fatalf("CheckoutCommit returned error: %v", err)
	}

	if len(executor.commands) != 3 {
		t.Fatalf("commands = %d, want 3", len(executor.commands))
	}
	for _, command := range executor.commands {
		for _, arg := range command.args {
			if strings.Contains(arg, "ghs_supersecrettoken") {
				t.Fatalf("arg %q carries the token; it must travel in the environment", arg)
			}
		}
		joined := strings.Join(command.env, "\n")
		if !strings.Contains(joined, "GIT_CONFIG_KEY_1=http.https://github.com/.extraheader") {
			t.Fatalf("env = %#v, want the scoped extraheader key", command.env)
		}
		if !strings.Contains(joined, "GIT_TERMINAL_PROMPT=0") {
			t.Fatalf("env = %#v, want prompts disabled", command.env)
		}
	}
}

// Without a credential the argv is exactly what it was before authentication
// existed, so public repositories keep working unchanged.
func TestLocalGitRunnerWithoutCredentialLeavesArgsUnchanged(t *testing.T) {
	t.Parallel()

	executor := &recordingCommandExecutor{}
	runner := NewLocalGitRunnerWithExecutor(executor)

	err := runner.Clone(context.Background(), "https://github.com/acme/public.git", "main", "/tmp/repo", GitCredential{})
	if err != nil {
		t.Fatalf("Clone returned error: %v", err)
	}

	wantArgs := []string{"clone", "--depth", "1", "--branch", "main", "https://github.com/acme/public.git", "/tmp/repo"}
	if !reflect.DeepEqual(executor.commands[0].args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", executor.commands[0].args, wantArgs)
	}
	if strings.Contains(strings.Join(executor.commands[0].env, "\n"), "GIT_CONFIG_COUNT") {
		t.Fatalf("env = %#v, want no git config keys", executor.commands[0].env)
	}
}
```

Then update the four existing tests to pass a credential and expect the env. In
`TestLocalGitRunnerClonesRef`, `TestLocalGitRunnerChecksOutExactCommit`, and
`TestLocalGitRunnerReportsWhichCheckoutStepFailed`, add `GitCredential{}` as the final argument,
and add `env: gitEnvironment(GitCredential{})` to every `recordedCommand` in each `want` literal.
`TestLocalGitRunnerWrapsCloneErrorsWithCommandOutput` gets `GitCredential{}` as the final
argument. `TestLocalGitRunnerResolvesHead` keeps its signature but its `want` gains the same
`env` field.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/runner/ -v`
Expected: FAIL to compile — `too many arguments in call to runner.Clone`.

- [ ] **Step 3: Update the runner**

In `internal/runner/git.go`, replace the interface and all three methods plus `combinedOutput`:

```go
// GitRunner is the subprocess boundary for source repository operations.
//
// Clone and CheckoutCommit take a credential because the remote may be private.
// It is a parameter rather than runner state on purpose: one runner serves every
// repository, and the token is scoped to a single one, so holding it would be
// both wrong and a data race. ResolveHead needs none -- it reads a local clone.
type GitRunner interface {
	Clone(ctx context.Context, repoURL string, ref string, dest string, credential GitCredential) error
	CheckoutCommit(ctx context.Context, repoURL string, commitSHA string, dest string, credential GitCredential) error
	ResolveHead(ctx context.Context, repoPath string) (string, error)
}
```

```go
func (runner *LocalGitRunner) Clone(ctx context.Context, repoURL string, ref string, dest string, credential GitCredential) error {
	environment := gitEnvironment(credential)
	output, err := runner.combinedOutput(ctx, environment, "git", "clone", "--depth", "1", "--branch", ref, repoURL, dest)
	if err != nil {
		return newGitCommandError(GitCommandClone, output, credential, err)
	}
	return nil
}
```

```go
func (runner *LocalGitRunner) CheckoutCommit(ctx context.Context, repoURL string, commitSHA string, dest string, credential GitCredential) error {
	steps := []struct {
		command GitCommand
		args    []string
	}{
		{GitCommandInit, []string{"init", "--quiet", dest}},
		{GitCommandFetch, []string{"-C", dest, "fetch", "--depth", "1", repoURL, commitSHA}},
		{GitCommandCheckout, []string{"-C", dest, "checkout", "--quiet", "FETCH_HEAD"}},
	}
	// Every step gets the environment, not only the networked fetch. Restricting
	// it to the fetch would be correct today and silently wrong the moment a
	// checkout reaches the network -- which it does for a repository using
	// git-LFS, whose smudge filter fetches during checkout.
	environment := gitEnvironment(credential)
	for _, step := range steps {
		output, err := runner.combinedOutput(ctx, environment, "git", step.args...)
		if err != nil {
			return newGitCommandError(step.command, output, credential, err)
		}
	}
	return nil
}
```

```go
func (runner *LocalGitRunner) ResolveHead(ctx context.Context, repoPath string) (string, error) {
	environment := gitEnvironment(GitCredential{})
	output, err := runner.combinedOutput(ctx, environment, "git", "-C", repoPath, "rev-parse", "HEAD")
	if err != nil {
		return "", newGitCommandError(GitCommandResolveHead, output, GitCredential{}, err)
	}
	return strings.TrimSpace(output), nil
}
```

```go
// combinedOutput runs a git command and returns its combined stdout/stderr,
// mirroring exec.Cmd.CombinedOutput on top of the shared CommandExecutor.
func (runner *LocalGitRunner) combinedOutput(ctx context.Context, env []string, name string, args ...string) (string, error) {
	var output bytes.Buffer
	err := runner.executor.Run(ctx, "", env, &output, &output, name, args...)
	return output.String(), err
}
```

- [ ] **Step 4: Update the four call sites and two test fakes**

`internal/activities/template_sync.go:82`:

```go
	if err := activities.git.Clone(ctx, repoURL, input.SourceRef, repoPath, runner.GitCredential{}); err != nil {
```

`internal/activities/template_run.go:167` and `:170`:

```go
	if commitSHA := strings.TrimSpace(input.ResolvedCommitSHA); commitSHA != "" {
		if err := git.CheckoutCommit(ctx, repoURL, commitSHA, sourcePath, runner.GitCredential{}); err != nil {
			return domain.FetchSourceActivityOutput{}, fmt.Errorf("checkout source commit %s: %w", commitSHA, err)
		}
	} else if err := git.Clone(ctx, repoURL, input.SourceRef, sourcePath, runner.GitCredential{}); err != nil {
		return domain.FetchSourceActivityOutput{}, fmt.Errorf("clone source: %w", err)
	}
```

`internal/activities/template_sync_test.go` — update `recordingGitRunner`:

```go
func (runner *recordingGitRunner) Clone(ctx context.Context, repoURL string, ref string, dest string, credential gitrunner.GitCredential) error {
	_ = ctx
	runner.credential = credential
	runner.repoURL = repoURL
	runner.ref = ref
	runner.dest = dest
	if runner.populate != nil {
		return runner.populate(dest)
	}
	return nil
}

// CheckoutCommit exists to satisfy runner.GitRunner and fails loudly: template
// sync is the path that resolves a ref to a commit in the first place, so it
// has no commit to check out and must clone the ref.
func (runner *recordingGitRunner) CheckoutCommit(context.Context, string, string, string, gitrunner.GitCredential) error {
	return errors.New("template sync must clone a ref, not check out a commit")
}
```

Add a `credential gitrunner.GitCredential` field to the `recordingGitRunner` struct and import
the runner package as `gitrunner "github.com/vishu42/tflive/internal/runner"`.

`internal/activities/template_run_test.go` — update `recordingSourceGitRunner` the same way: add
a `credential gitrunner.GitCredential` field, add the parameter to both `Clone` and
`CheckoutCommit`, and record it.

- [ ] **Step 5: Run the full suite**

Run: `go build ./... && go test ./... `
Expected: PASS everywhere.

- [ ] **Step 6: Commit**

```bash
git add internal/runner internal/activities
git commit -m "refactor: thread a git credential through the git runner boundary"
```

---

## Task 4: GitHub App key parsing and API client

**Files:**
- Create: `internal/githubapp/key.go`, `internal/githubapp/key_test.go`
- Create: `internal/githubapp/errors.go`
- Create: `internal/githubapp/client.go`, `internal/githubapp/client_test.go`
- Modify: `internal/githubapp/doc.go` (extend the package comment)

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces:
  - `githubapp.ParsePrivateKey(value string) (*rsa.PrivateKey, error)`
  - `githubapp.ErrAppNotInstalled` sentinel
  - `githubapp.Token{Value string; ExpiresAt time.Time}`
  - `githubapp.NewClient(appID string, privateKey *rsa.PrivateKey, options ...ClientOption) *Client`
  - `githubapp.WithBaseURL(string) ClientOption`, `WithHTTPClient(*http.Client) ClientOption`,
    `WithClock(func() time.Time) ClientOption`
  - `(*Client).InstallationForRepo(ctx context.Context, owner, repo string) (int64, error)`
  - `(*Client).MintToken(ctx context.Context, installationID int64, repo string) (Token, error)`

- [ ] **Step 1: Write the failing key tests**

Create `internal/githubapp/key_test.go`:

```go
package githubapp

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"strings"
	"testing"
)

func pkcs1PEM(t *testing.T) (string, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	encoded := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	return string(encoded), key
}

// GitHub hands out PKCS#1 ("BEGIN RSA PRIVATE KEY").
func TestParsePrivateKeyAcceptsPKCS1PEM(t *testing.T) {
	t.Parallel()

	encoded, key := pkcs1PEM(t)

	parsed, err := ParsePrivateKey(encoded)
	if err != nil {
		t.Fatalf("ParsePrivateKey returned error: %v", err)
	}
	if !parsed.Equal(key) {
		t.Fatal("parsed key does not match the original")
	}
}

func TestParsePrivateKeyAcceptsPKCS8PEM(t *testing.T) {
	t.Parallel()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal pkcs8: %v", err)
	}
	encoded := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})

	parsed, err := ParsePrivateKey(string(encoded))
	if err != nil {
		t.Fatalf("ParsePrivateKey returned error: %v", err)
	}
	if !parsed.Equal(key) {
		t.Fatal("parsed key does not match the original")
	}
}

// A multi-line PEM is awkward to carry in a .env file, so a base64 blob of the
// whole PEM is accepted as an equivalent single-line form.
func TestParsePrivateKeyAcceptsBase64EncodedPEM(t *testing.T) {
	t.Parallel()

	encoded, key := pkcs1PEM(t)
	blob := base64.StdEncoding.EncodeToString([]byte(encoded))

	parsed, err := ParsePrivateKey(blob)
	if err != nil {
		t.Fatalf("ParsePrivateKey returned error: %v", err)
	}
	if !parsed.Equal(key) {
		t.Fatal("parsed key does not match the original")
	}
}

func TestParsePrivateKeyRejectsGarbage(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"", "not a key", "-----BEGIN RSA PRIVATE KEY-----\nnope\n-----END RSA PRIVATE KEY-----"} {
		if _, err := ParsePrivateKey(value); err == nil {
			t.Fatalf("ParsePrivateKey(%q) returned no error", value)
		}
	}
}

// The key must never be echoed back in an error; error strings reach logs.
func TestParsePrivateKeyErrorOmitsKeyMaterial(t *testing.T) {
	t.Parallel()

	_, err := ParsePrivateKey("-----BEGIN RSA PRIVATE KEY-----\nSECRETMATERIAL\n-----END RSA PRIVATE KEY-----")
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "SECRETMATERIAL") {
		t.Fatalf("error = %q, want no key material", err)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/githubapp/ -v`
Expected: FAIL to compile — `undefined: ParsePrivateKey`.

- [ ] **Step 3: Implement key parsing and the error sentinel**

Create `internal/githubapp/key.go`:

```go
package githubapp

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
)

// ParsePrivateKey reads a GitHub App's RSA signing key.
//
// Two encodings are accepted for the same bytes: a PEM document as GitHub
// downloads it, and a base64 blob of that document. The second exists because a
// multi-line value is awkward to carry through a .env file or a container
// environment variable, where a single line is the only reliable shape.
//
// GitHub issues PKCS#1 keys; PKCS#8 is accepted too, since a key round-tripped
// through other tooling often comes back in that form.
//
// No error returned here includes any part of the input: these errors reach
// startup logs.
func ParsePrivateKey(value string) (*rsa.PrivateKey, error) {
	raw := []byte(strings.TrimSpace(value))
	if len(raw) == 0 {
		return nil, errors.New("private key is empty")
	}
	if !strings.HasPrefix(string(raw), "-----BEGIN") {
		decoded, err := base64.StdEncoding.DecodeString(string(raw))
		if err != nil {
			return nil, errors.New("private key is neither PEM nor base64-encoded PEM")
		}
		raw = decoded
	}

	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, errors.New("no PEM block found in private key")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("private key is not a valid PKCS#1 or PKCS#8 RSA key")
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("private key is %T, want an RSA key", parsed)
	}
	return key, nil
}
```

Create `internal/githubapp/errors.go`:

```go
package githubapp

import "errors"

// ErrAppNotInstalled reports that the GitHub App has no installation covering
// the requested repository.
//
// It is the single most likely failure in practice -- someone registers a
// template before an org admin installs the App on that repo -- so callers
// match on it to produce an actionable message instead of surfacing a raw HTTP
// status or, worse, a git authentication error several steps later.
var ErrAppNotInstalled = errors.New("github app is not installed on the repository")
```

Replace `internal/githubapp/doc.go` contents:

```go
// Package githubapp authenticates tflive to GitHub as a GitHub App.
//
// It signs a short-lived App JWT with the App's RSA key, resolves which
// installation covers a given repository, and exchanges the JWT for a
// repo-scoped, read-only installation token that expires within the hour. That
// token is what the worker hands to git to clone a private repository.
//
// Nothing here is persisted: an installation is resolved on demand rather than
// stored, so no schema or lifecycle exists to keep in sync with GitHub.
package githubapp
```

- [ ] **Step 4: Run to verify the key tests pass**

Run: `go test ./internal/githubapp/ -v`
Expected: PASS.

- [ ] **Step 5: Write the failing client tests**

Create `internal/githubapp/client_test.go`:

```go
package githubapp

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwt"
)

func testKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return key
}

// The JWT is the App's only proof of identity. GitHub rejects an exp more than
// ten minutes out, and a clock a second fast makes an iat of "now" invalid, so
// both bounds are asserted rather than assumed.
func TestClientSignsAppJWTWithinGitHubBounds(t *testing.T) {
	t.Parallel()

	key := testKey(t)
	issued := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	client := NewClient("12345", key, WithClock(func() time.Time { return issued }))

	raw, err := client.appJWT()
	if err != nil {
		t.Fatalf("appJWT returned error: %v", err)
	}

	token, err := jwt.Parse([]byte(raw), jwt.WithKey(jwa.RS256(), key.Public()))
	if err != nil {
		t.Fatalf("parse jwt: %v", err)
	}
	issuer, ok := token.Issuer()
	if !ok || issuer != "12345" {
		t.Fatalf("issuer = %q, want 12345", issuer)
	}
	issuedAt, ok := token.IssuedAt()
	if !ok || !issuedAt.Before(issued) {
		t.Fatalf("iat = %v, want before %v to absorb clock skew", issuedAt, issued)
	}
	expiration, ok := token.Expiration()
	if !ok {
		t.Fatal("exp is missing")
	}
	if expiration.Sub(issued) > 10*time.Minute {
		t.Fatalf("exp = %v, more than 10 minutes after %v", expiration, issued)
	}
}

func TestInstallationForRepoReturnsID(t *testing.T) {
	t.Parallel()

	var gotPath, gotAuthPrefix string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if len(r.Header.Get("Authorization")) > 7 {
			gotAuthPrefix = r.Header.Get("Authorization")[:7]
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 48291})
	}))
	defer server.Close()

	client := NewClient("12345", testKey(t), WithBaseURL(server.URL))

	id, err := client.InstallationForRepo(context.Background(), "acme", "private-infra")
	if err != nil {
		t.Fatalf("InstallationForRepo returned error: %v", err)
	}
	if id != 48291 {
		t.Fatalf("id = %d, want 48291", id)
	}
	if gotPath != "/repos/acme/private-infra/installation" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotAuthPrefix != "Bearer " {
		t.Fatalf("auth prefix = %q, want Bearer", gotAuthPrefix)
	}
}

// A 404 here means the App was never installed on that repo. It must be
// distinguishable, because it is the one failure a user can actually act on.
func TestInstallationForRepoReportsNotInstalled(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
	}))
	defer server.Close()

	client := NewClient("12345", testKey(t), WithBaseURL(server.URL))

	_, err := client.InstallationForRepo(context.Background(), "acme", "missing")
	if !errors.Is(err, ErrAppNotInstalled) {
		t.Fatalf("error = %v, want ErrAppNotInstalled", err)
	}
}

// The minted token is narrowed to one repository and to reading contents, so a
// leak exposes that repo's source and nothing else in the installation.
func TestMintTokenRequestsRepoScopedReadOnlyToken(t *testing.T) {
	t.Parallel()

	var body struct {
		Repositories []string          `json:"repositories"`
		Permissions  map[string]string `json:"permissions"`
	}
	var gotPath string
	expiry := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":      "ghs_minted",
			"expires_at": expiry.Format(time.RFC3339),
		})
	}))
	defer server.Close()

	client := NewClient("12345", testKey(t), WithBaseURL(server.URL))

	token, err := client.MintToken(context.Background(), 48291, "private-infra")
	if err != nil {
		t.Fatalf("MintToken returned error: %v", err)
	}
	if token.Value != "ghs_minted" {
		t.Fatalf("token = %q, want ghs_minted", token.Value)
	}
	if !token.ExpiresAt.Equal(expiry) {
		t.Fatalf("expires = %v, want %v", token.ExpiresAt, expiry)
	}
	if gotPath != "/app/installations/48291/access_tokens" {
		t.Fatalf("path = %q", gotPath)
	}
	if len(body.Repositories) != 1 || body.Repositories[0] != "private-infra" {
		t.Fatalf("repositories = %#v, want [private-infra]", body.Repositories)
	}
	if body.Permissions["contents"] != "read" {
		t.Fatalf("permissions = %#v, want contents:read", body.Permissions)
	}
}

// A failure response body can echo request material; it must not reach an error
// string unbounded, and the minted token must never appear in one.
func TestMintTokenFailureOmitsTokenAndBoundsBody(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"Bad credentials"}`, http.StatusUnauthorized)
	}))
	defer server.Close()

	client := NewClient("12345", testKey(t), WithBaseURL(server.URL))

	_, err := client.MintToken(context.Background(), 48291, "private-infra")
	if err == nil {
		t.Fatal("expected an error")
	}
	if len(err.Error()) > 1024 {
		t.Fatalf("error length = %d, want bounded", len(err.Error()))
	}
}
```

- [ ] **Step 6: Run to verify it fails**

Run: `go test ./internal/githubapp/ -run 'Client|Installation|MintToken' -v`
Expected: FAIL to compile — `undefined: NewClient`.

- [ ] **Step 7: Implement the client**

Create `internal/githubapp/client.go`:

```go
package githubapp

import (
	"bytes"
	"context"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwt"
)

const (
	defaultAPIBaseURL  = "https://api.github.com"
	defaultHTTPTimeout = 15 * time.Second
	// GitHub rejects an App JWT whose exp is more than ten minutes past its iat.
	// Nine leaves room for the request itself without courting the boundary.
	appJWTLifetime = 9 * time.Minute
	// The iat is backdated so a worker clock running slightly fast does not
	// produce a token GitHub considers issued in the future.
	appJWTBackdate = time.Minute
	// Enough of an error body to diagnose, bounded so a hostile or broken
	// response cannot flood a log.
	maxErrorBody = 512
)

// Token is a minted installation access token and the moment it stops working.
//
// ExpiresAt comes from GitHub rather than being computed locally: the documented
// lifetime is an hour, but a value read from the response stays correct if that
// ever changes.
type Token struct {
	Value     string
	ExpiresAt time.Time
}

// Client calls the GitHub App endpoints tflive needs: which installation covers
// a repository, and a token for it.
type Client struct {
	baseURL    string
	appID      string
	privateKey *rsa.PrivateKey
	http       *http.Client
	now        func() time.Time
}

type ClientOption func(*Client)

// WithBaseURL points the client at another API root. Tests use it; production
// does not.
func WithBaseURL(baseURL string) ClientOption {
	return func(client *Client) {
		if baseURL != "" {
			client.baseURL = baseURL
		}
	}
}

func WithHTTPClient(httpClient *http.Client) ClientOption {
	return func(client *Client) {
		if httpClient != nil {
			client.http = httpClient
		}
	}
}

// WithClock replaces the time source, so JWT bounds can be asserted exactly.
func WithClock(now func() time.Time) ClientOption {
	return func(client *Client) {
		if now != nil {
			client.now = now
		}
	}
}

func NewClient(appID string, privateKey *rsa.PrivateKey, options ...ClientOption) *Client {
	client := &Client{
		baseURL:    defaultAPIBaseURL,
		appID:      appID,
		privateKey: privateKey,
		http:       &http.Client{Timeout: defaultHTTPTimeout},
		now:        time.Now,
	}
	for _, option := range options {
		option(client)
	}
	return client
}

// appJWT signs the App's identity assertion. It authenticates the App itself,
// not any installation, and opens only the endpoints below -- it can read no
// repository content.
func (client *Client) appJWT() (string, error) {
	now := client.now()
	token, err := jwt.NewBuilder().
		Issuer(client.appID).
		IssuedAt(now.Add(-appJWTBackdate)).
		Expiration(now.Add(appJWTLifetime)).
		Build()
	if err != nil {
		return "", fmt.Errorf("build app jwt: %w", err)
	}
	signed, err := jwt.Sign(token, jwt.WithKey(jwa.RS256(), client.privateKey))
	if err != nil {
		return "", fmt.Errorf("sign app jwt: %w", err)
	}
	return string(signed), nil
}

// InstallationForRepo returns the installation covering owner/repo.
//
// Resolving per repository is what makes multiple organisations work without
// any stored state: one App installed on many accounts yields a different
// installation here for each, and each mints its own token.
func (client *Client) InstallationForRepo(ctx context.Context, owner string, repo string) (int64, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/installation", client.baseURL, owner, repo)
	response, err := client.do(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()

	if response.StatusCode == http.StatusNotFound {
		return 0, fmt.Errorf("%w: %s/%s", ErrAppNotInstalled, owner, repo)
	}
	if response.StatusCode != http.StatusOK {
		return 0, client.statusError("resolve installation", response)
	}

	var payload struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maxErrorBody<<4)).Decode(&payload); err != nil {
		return 0, fmt.Errorf("decode installation: %w", err)
	}
	if payload.ID == 0 {
		return 0, fmt.Errorf("resolve installation: response carried no id")
	}
	return payload.ID, nil
}

// MintToken exchanges the App JWT for an installation token scoped to one
// repository with read-only access to its contents.
//
// The narrowing is deliberate: an installation may cover hundreds of
// repositories with write access, and this token is used for exactly one clone.
func (client *Client) MintToken(ctx context.Context, installationID int64, repo string) (Token, error) {
	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", client.baseURL, installationID)
	body, err := json.Marshal(map[string]any{
		"repositories": []string{repo},
		"permissions":  map[string]string{"contents": "read"},
	})
	if err != nil {
		return Token{}, fmt.Errorf("encode token request: %w", err)
	}

	response, err := client.do(ctx, http.MethodPost, url, body)
	if err != nil {
		return Token{}, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusCreated && response.StatusCode != http.StatusOK {
		return Token{}, client.statusError("mint installation token", response)
	}

	var payload struct {
		Token     string `json:"token"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maxErrorBody<<4)).Decode(&payload); err != nil {
		return Token{}, fmt.Errorf("decode installation token: %w", err)
	}
	if payload.Token == "" {
		return Token{}, fmt.Errorf("mint installation token: response carried no token")
	}
	expiresAt, err := time.Parse(time.RFC3339, payload.ExpiresAt)
	if err != nil {
		return Token{}, fmt.Errorf("parse installation token expiry: %w", err)
	}
	return Token{Value: payload.Token, ExpiresAt: expiresAt.UTC()}, nil
}

func (client *Client) do(ctx context.Context, method string, url string, body []byte) (*http.Response, error) {
	assertion, err := client.appJWT()
	if err != nil {
		return nil, err
	}

	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, fmt.Errorf("build github request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+assertion)
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	response, err := client.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("github request failed: %w", err)
	}
	return response, nil
}

// statusError renders a bounded slice of the failure body. GitHub echoes little
// of the request back, but the bound means a hostile or broken response cannot
// flood a log through an error string.
func (client *Client) statusError(action string, response *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(response.Body, maxErrorBody))
	return fmt.Errorf("%s: unexpected status %s: %s", action, response.Status, bytes.TrimSpace(body))
}
```

- [ ] **Step 8: Run to verify tests pass**

Run: `go test ./internal/githubapp/ -v`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/githubapp
git commit -m "feat: add a GitHub App client that mints repo-scoped installation tokens"
```

---

## Task 5: `TokenSource` with an expiry-aware cache

**Files:**
- Create: `internal/githubapp/tokensource.go`, `internal/githubapp/tokensource_test.go`

**Interfaces:**
- Consumes: `Client`, `Token`, `ErrAppNotInstalled` from Task 4.
- Produces:
  - `githubapp.NewTokenSource(client *Client, options ...TokenSourceOption) *TokenSource`
  - `githubapp.WithTokenSourceClock(func() time.Time) TokenSourceOption`
  - `(*TokenSource).Token(ctx context.Context, owner, repo string) (string, error)` — returns
    `("", nil)` when the source is nil or has no client.

- [ ] **Step 1: Write the failing tests**

Create `internal/githubapp/tokensource_test.go`:

```go
package githubapp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func tokenServer(t *testing.T, mints *int64, expiry time.Time) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/installation") {
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 48291})
			return
		}
		atomic.AddInt64(mints, 1)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":      "ghs_minted",
			"expires_at": expiry.Format(time.RFC3339),
		})
	}))
}

// A run and a registration against the same repo minutes apart should not each
// pay two API calls against the App's hourly budget.
func TestTokenSourceCachesUntilNearExpiry(t *testing.T) {
	t.Parallel()

	var mints int64
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	server := tokenServer(t, &mints, now.Add(time.Hour))
	defer server.Close()

	clock := now
	source := NewTokenSource(
		NewClient("12345", testKey(t), WithBaseURL(server.URL)),
		WithTokenSourceClock(func() time.Time { return clock }),
	)

	for i := 0; i < 3; i++ {
		token, err := source.Token(context.Background(), "acme", "private-infra")
		if err != nil {
			t.Fatalf("Token returned error: %v", err)
		}
		if token != "ghs_minted" {
			t.Fatalf("token = %q, want ghs_minted", token)
		}
		clock = clock.Add(10 * time.Minute)
	}
	if got := atomic.LoadInt64(&mints); got != 1 {
		t.Fatalf("mints = %d, want 1", got)
	}
}

// A token handed out moments before it expires is a clone that fails halfway.
// The margin is what stops that.
func TestTokenSourceRemintsInsideTheExpiryMargin(t *testing.T) {
	t.Parallel()

	var mints int64
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	server := tokenServer(t, &mints, now.Add(time.Hour))
	defer server.Close()

	clock := now
	source := NewTokenSource(
		NewClient("12345", testKey(t), WithBaseURL(server.URL)),
		WithTokenSourceClock(func() time.Time { return clock }),
	)

	if _, err := source.Token(context.Background(), "acme", "private-infra"); err != nil {
		t.Fatalf("Token returned error: %v", err)
	}
	// 58 minutes in, the cached token has two minutes left -- inside the margin.
	clock = clock.Add(58 * time.Minute)
	if _, err := source.Token(context.Background(), "acme", "private-infra"); err != nil {
		t.Fatalf("Token returned error: %v", err)
	}
	if got := atomic.LoadInt64(&mints); got != 2 {
		t.Fatalf("mints = %d, want 2", got)
	}
}

// Tokens are scoped to one repository, so one repo's token can never serve
// another even within the same installation.
func TestTokenSourceKeysCacheByRepository(t *testing.T) {
	t.Parallel()

	var mints int64
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	server := tokenServer(t, &mints, now.Add(time.Hour))
	defer server.Close()

	source := NewTokenSource(
		NewClient("12345", testKey(t), WithBaseURL(server.URL)),
		WithTokenSourceClock(func() time.Time { return now }),
	)

	if _, err := source.Token(context.Background(), "acme", "repo-a"); err != nil {
		t.Fatalf("Token returned error: %v", err)
	}
	if _, err := source.Token(context.Background(), "acme", "repo-b"); err != nil {
		t.Fatalf("Token returned error: %v", err)
	}
	if got := atomic.LoadInt64(&mints); got != 2 {
		t.Fatalf("mints = %d, want 2", got)
	}
}

// An unconfigured deployment must clone public repositories exactly as before,
// so the disabled source is a success returning no credential, not an error.
func TestNilTokenSourceReturnsNoCredential(t *testing.T) {
	t.Parallel()

	var source *TokenSource

	token, err := source.Token(context.Background(), "acme", "public")
	if err != nil {
		t.Fatalf("Token returned error: %v", err)
	}
	if token != "" {
		t.Fatalf("token = %q, want empty", token)
	}
}

func TestTokenSourcePropagatesNotInstalled(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
	}))
	defer server.Close()

	source := NewTokenSource(NewClient("12345", testKey(t), WithBaseURL(server.URL)))

	_, err := source.Token(context.Background(), "acme", "missing")
	if !errors.Is(err, ErrAppNotInstalled) {
		t.Fatalf("error = %v, want ErrAppNotInstalled", err)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/githubapp/ -run TokenSource -v`
Expected: FAIL to compile — `undefined: NewTokenSource`.

- [ ] **Step 3: Implement the token source**

Create `internal/githubapp/tokensource.go`:

```go
package githubapp

import (
	"context"
	"sync"
	"time"
)

// expiryMargin is how long before a token's stated expiry it stops being
// handed out. A token with seconds left is a clone that fails partway through
// for no reason a user could diagnose.
const expiryMargin = 5 * time.Minute

// TokenSource hands out installation tokens for repositories, minting on demand
// and caching until each is close to expiring.
//
// A nil TokenSource is valid and yields no credential: that is how a deployment
// with no GitHub App configured keeps cloning public repositories unchanged.
type TokenSource struct {
	client *Client
	now    func() time.Time

	mu     sync.Mutex
	tokens map[string]Token
}

type TokenSourceOption func(*TokenSource)

// WithTokenSourceClock replaces the time source so cache expiry can be tested
// without sleeping.
func WithTokenSourceClock(now func() time.Time) TokenSourceOption {
	return func(source *TokenSource) {
		if now != nil {
			source.now = now
		}
	}
}

func NewTokenSource(client *Client, options ...TokenSourceOption) *TokenSource {
	source := &TokenSource{
		client: client,
		now:    time.Now,
		tokens: make(map[string]Token),
	}
	for _, option := range options {
		option(source)
	}
	return source
}

// Token returns an installation token that can read owner/repo, or an empty
// string when no GitHub App is configured.
//
// The cache is keyed by repository rather than by installation because the
// minted token is itself repository-scoped -- one repo's token cannot serve
// another even when the same installation covers both.
func (source *TokenSource) Token(ctx context.Context, owner string, repo string) (string, error) {
	if source == nil || source.client == nil {
		return "", nil
	}

	key := owner + "/" + repo
	if token, ok := source.cached(key); ok {
		return token, nil
	}

	installationID, err := source.client.InstallationForRepo(ctx, owner, repo)
	if err != nil {
		return "", err
	}
	token, err := source.client.MintToken(ctx, installationID, repo)
	if err != nil {
		return "", err
	}

	source.mu.Lock()
	source.tokens[key] = token
	source.mu.Unlock()
	return token.Value, nil
}

func (source *TokenSource) cached(key string) (string, bool) {
	source.mu.Lock()
	defer source.mu.Unlock()

	token, ok := source.tokens[key]
	if !ok {
		return "", false
	}
	if !source.now().Add(expiryMargin).Before(token.ExpiresAt) {
		delete(source.tokens, key)
		return "", false
	}
	return token.Value, true
}
```

- [ ] **Step 4: Run to verify tests pass**

Run: `go test ./internal/githubapp/ -race -v`
Expected: PASS, no race warnings.

- [ ] **Step 5: Commit**

```bash
git add internal/githubapp/tokensource.go internal/githubapp/tokensource_test.go
git commit -m "feat: cache GitHub installation tokens until they near expiry"
```

---

## Task 6: Worker configuration

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

**Interfaces:**
- Consumes: `githubapp.ParsePrivateKey` from Task 4.
- Produces:
  - `config.GitHubAppConfig{AppID string; PrivateKey Secret}` with method `Enabled() bool`
  - `WorkerConfig.GitHubApp GitHubAppConfig`

- [ ] **Step 1: Write the failing tests**

Append to `internal/config/config_test.go`:

```go
func TestLoadWorkerConfigReadsGitHubApp(t *testing.T) {
	t.Parallel()

	encoded, _ := testRSAPrivateKeyPEM(t)
	cfg, err := LoadWorkerConfig(withValidWorkerEnv(map[string]string{
		"GITHUB_APP_ID":          " 12345 ",
		"GITHUB_APP_PRIVATE_KEY": encoded,
	}))
	if err != nil {
		t.Fatalf("LoadWorkerConfig returned error: %v", err)
	}
	if !cfg.GitHubApp.Enabled() {
		t.Fatal("GitHubApp should be enabled")
	}
	if cfg.GitHubApp.AppID != "12345" {
		t.Fatalf("AppID = %q, want 12345", cfg.GitHubApp.AppID)
	}
}

// An unconfigured App is the supported default, not a misconfiguration: the
// deployment simply cannot reach private repositories.
func TestLoadWorkerConfigAllowsAbsentGitHubApp(t *testing.T) {
	t.Parallel()

	cfg, err := LoadWorkerConfig(withValidWorkerEnv(nil))
	if err != nil {
		t.Fatalf("LoadWorkerConfig returned error: %v", err)
	}
	if cfg.GitHubApp.Enabled() {
		t.Fatal("GitHubApp should be disabled")
	}
}

// Half a configuration is always a mistake, and one that would otherwise
// surface as a puzzling clone failure much later.
func TestLoadWorkerConfigRejectsPartialGitHubApp(t *testing.T) {
	t.Parallel()

	encoded, _ := testRSAPrivateKeyPEM(t)
	for name, env := range map[string]map[string]string{
		"id without key": {"GITHUB_APP_ID": "12345"},
		"key without id": {"GITHUB_APP_PRIVATE_KEY": encoded},
	} {
		if _, err := LoadWorkerConfig(withValidWorkerEnv(env)); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("%s: error = %v, want ErrInvalidConfig", name, err)
		}
	}
}

func TestLoadWorkerConfigRejectsMalformedGitHubAppValues(t *testing.T) {
	t.Parallel()

	encoded, _ := testRSAPrivateKeyPEM(t)
	for name, env := range map[string]map[string]string{
		"non-numeric app id": {"GITHUB_APP_ID": "not-a-number", "GITHUB_APP_PRIVATE_KEY": encoded},
		"malformed key":      {"GITHUB_APP_ID": "12345", "GITHUB_APP_PRIVATE_KEY": "not-a-key"},
	} {
		if _, err := LoadWorkerConfig(withValidWorkerEnv(env)); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("%s: error = %v, want ErrInvalidConfig", name, err)
		}
	}
}

// The key must not be recoverable from a config value printed in a log.
func TestGitHubAppConfigRedactsPrivateKey(t *testing.T) {
	t.Parallel()

	encoded, _ := testRSAPrivateKeyPEM(t)
	cfg := GitHubAppConfig{AppID: "12345", PrivateKey: newSecret(encoded)}

	if rendered := fmt.Sprintf("%v", cfg.PrivateKey); rendered != "[REDACTED]" {
		t.Fatalf("PrivateKey = %q, want [REDACTED]", rendered)
	}
}

func testRSAPrivateKeyPEM(t *testing.T) (string, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	encoded := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	return string(encoded), key
}

// withValidWorkerEnv returns a getenv covering the worker's required settings,
// with overrides layered on top.
func withValidWorkerEnv(overrides map[string]string) func(string) string {
	base := map[string]string{
		"DATABASE_URL":     "postgres://user:pass@localhost:5432/db?sslmode=disable",
		"TEMPORAL_ADDRESS": "localhost:7233",
	}
	return func(key string) string {
		if value, ok := overrides[key]; ok {
			return value
		}
		return base[key]
	}
}
```

Add `crypto/rand`, `crypto/rsa`, `crypto/x509`, `encoding/pem`, and `fmt` to the test imports.

> If `LoadWorkerConfig` in this repo requires OpenFGA settings that `withValidWorkerEnv` omits,
> extend `base` with whatever `loadOpenFGAConfig` demands — read `internal/config/auth.go` and
> copy the keys the existing worker-config tests already set. Do not weaken the loader to make
> the test pass.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/config/ -run GitHubApp -v`
Expected: FAIL to compile — `cfg.GitHubApp undefined`.

- [ ] **Step 3: Implement the loader**

In `internal/config/config.go`, add `"strconv"` and
`"github.com/vishu42/tflive/internal/githubapp"` to the imports, add the field to
`WorkerConfig`:

```go
type WorkerConfig struct {
	DatabaseURL             string
	TemporalAddress         string
	TemporalNamespace       string
	TemporalTaskQueue       string
	WorkerRunRoot           string
	ArtifactStore           ArtifactStoreConfig
	OpenFGA                 OpenFGAConfig
	CredentialEncryptionKey Secret
	GitHubApp               GitHubAppConfig
}
```

Add the type and loader:

```go
// GitHubAppConfig identifies the GitHub App whose installations grant access to
// private template repositories.
//
// It lives on the worker alone: the API process never clones, so it has no use
// for a signing key and should not hold one.
type GitHubAppConfig struct {
	AppID      string
	PrivateKey Secret
}

// Enabled reports whether a GitHub App is configured. When it is not, source
// fetches stay unauthenticated and only public repositories are reachable.
func (cfg GitHubAppConfig) Enabled() bool {
	return cfg.AppID != "" && !cfg.PrivateKey.Empty()
}

// loadGitHubAppConfig validates GITHUB_APP_ID and GITHUB_APP_PRIVATE_KEY the
// same way loadCredentialEncryptionKey validates its key: by constructing the
// thing it backs, so a malformed value fails at startup rather than at the
// first clone.
//
// Both absent means the App is not configured, which is a supported deployment
// and not an error. Exactly one present is always a mistake -- and one that
// would otherwise surface much later as an unexplained authentication failure.
func loadGitHubAppConfig(getenv func(string) string) (GitHubAppConfig, error) {
	appID := strings.TrimSpace(getenv("GITHUB_APP_ID"))
	privateKey := newSecret(strings.TrimSpace(getenv("GITHUB_APP_PRIVATE_KEY")))

	if appID == "" && privateKey.Empty() {
		return GitHubAppConfig{}, nil
	}
	if appID == "" {
		return GitHubAppConfig{}, fmt.Errorf("%w: GITHUB_APP_ID is required when GITHUB_APP_PRIVATE_KEY is set", ErrInvalidConfig)
	}
	if privateKey.Empty() {
		return GitHubAppConfig{}, fmt.Errorf("%w: GITHUB_APP_PRIVATE_KEY is required when GITHUB_APP_ID is set", ErrInvalidConfig)
	}
	if _, err := strconv.ParseInt(appID, 10, 64); err != nil {
		return GitHubAppConfig{}, fmt.Errorf("%w: GITHUB_APP_ID must be the App's numeric id", ErrInvalidConfig)
	}
	if _, err := githubapp.ParsePrivateKey(privateKey.Value()); err != nil {
		return GitHubAppConfig{}, fmt.Errorf("%w: GITHUB_APP_PRIVATE_KEY must be an RSA private key as PEM or base64-encoded PEM", ErrInvalidConfig)
	}
	return GitHubAppConfig{AppID: appID, PrivateKey: privateKey}, nil
}
```

In `LoadWorkerConfig`, after the `credentialKey` block:

```go
	gitHubApp, err := loadGitHubAppConfig(getenv)
	if err != nil {
		return WorkerConfig{}, err
	}
	cfg.GitHubApp = gitHubApp
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/config/ -v && go build ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config
git commit -m "feat: load GitHub App credentials into the worker config"
```

---

## Task 7: Authenticate the two activity fetch paths

**Files:**
- Modify: `internal/activities/template_sync.go`
- Modify: `internal/activities/template_run.go`
- Modify: `internal/activities/template_sync_test.go`, `internal/activities/template_run_test.go`

**Interfaces:**
- Consumes: `runner.NewGitCredential` (Task 2), the credential parameter on `GitRunner`
  (Task 3), `githubapp.ErrAppNotInstalled` and `(*TokenSource).Token` (Tasks 4-5).
- Produces:
  - `activities.GitHubTokenSource` interface — `Token(ctx context.Context, owner, repo string) (string, error)`
  - `activities.WithTemplateSyncTokenSource(GitHubTokenSource) TemplateSyncOption`
  - `NewTemplateRunActivitiesWithCredentials(recorder, runRoot, logStore, credentialReader, credentialDecryptor, tokens GitHubTokenSource, terraformRunners ...TerraformRunner)`
  - `gitHubRepoURL(owner, repo string) (string, error)` replacing `publicGitHubRepoURL`

- [ ] **Step 1: Write the failing tests**

Append to `internal/activities/template_sync_test.go`:

```go
// The credential must reach the clone. Without this the whole feature is a
// no-op that still passes every other test.
func TestSyncTemplatePassesInstallationTokenToClone(t *testing.T) {
	t.Parallel()

	store := &recordingTemplateSyncStore{}
	gitRunner := &recordingGitRunner{
		commitSHA: "abc123",
		populate: func(repoPath string) error {
			return os.MkdirAll(filepath.Join(repoPath, "modules", "vpc"), 0o700)
		},
	}
	activities := NewTemplateSyncActivities(
		store,
		WithTemplateSyncGitRunner(gitRunner),
		WithTemplateSyncTempRoot(t.TempDir()),
		WithTemplateSyncTokenSource(stubTokenSource{token: "ghs_minted"}),
	)

	if _, err := activities.SyncTemplate(context.Background(), domain.TemplateSyncActivityInput{
		TenantID:  domain.TenantID("tenant_123"),
		RepoOwner: "acme",
		RepoName:  "private-infra",
		SourceRef: "main",
		RootPath:  "modules/vpc",
	}); err != nil {
		t.Fatalf("SyncTemplate returned error: %v", err)
	}

	if gitRunner.credential != gitrunner.NewGitCredential("ghs_minted") {
		t.Fatal("clone did not receive the installation credential")
	}
}

// The one failure a user can act on must say what to do, and must be classified
// non-retryable -- retrying an uninstalled App four times helps nobody.
func TestSyncTemplateReportsUninstalledApp(t *testing.T) {
	t.Parallel()

	store := &recordingTemplateSyncStore{}
	activities := NewTemplateSyncActivities(
		store,
		WithTemplateSyncGitRunner(&recordingGitRunner{commitSHA: "abc123"}),
		WithTemplateSyncTempRoot(t.TempDir()),
		WithTemplateSyncTokenSource(stubTokenSource{err: githubapp.ErrAppNotInstalled}),
	)

	output, err := activities.SyncTemplate(context.Background(), domain.TemplateSyncActivityInput{
		TenantID:  domain.TenantID("tenant_123"),
		RepoOwner: "acme",
		RepoName:  "private-infra",
		SourceRef: "main",
		RootPath:  "modules/vpc",
	})
	if err != nil {
		t.Fatalf("SyncTemplate returned error: %v", err)
	}
	if output.Status != domain.TemplateRegistrationInvalid {
		t.Fatalf("status = %q, want %q", output.Status, domain.TemplateRegistrationInvalid)
	}
	if !strings.Contains(output.ErrorSummary, "not installed") {
		t.Fatalf("summary = %q, want it to name the missing installation", output.ErrorSummary)
	}
	if !strings.Contains(output.ErrorSummary, "acme/private-infra") {
		t.Fatalf("summary = %q, want it to name the repository", output.ErrorSummary)
	}
}

// An owner or repo carrying URL metacharacters must be rejected at the boundary
// rather than folded into a request URL.
func TestSyncTemplateRejectsUnsafeRepoIdentifiers(t *testing.T) {
	t.Parallel()

	activities := NewTemplateSyncActivities(
		&recordingTemplateSyncStore{},
		WithTemplateSyncGitRunner(&recordingGitRunner{commitSHA: "abc123"}),
		WithTemplateSyncTempRoot(t.TempDir()),
	)

	for _, owner := range []string{"acme/evil", "acme?x=1", "acme#frag", "acme corp", "acme\nx"} {
		output, err := activities.SyncTemplate(context.Background(), domain.TemplateSyncActivityInput{
			TenantID:  domain.TenantID("tenant_123"),
			RepoOwner: owner,
			RepoName:  "infra",
			SourceRef: "main",
			RootPath:  ".",
		})
		if err != nil {
			t.Fatalf("SyncTemplate(%q) returned error: %v", owner, err)
		}
		if output.Status != domain.TemplateRegistrationInvalid {
			t.Fatalf("SyncTemplate(%q) status = %q, want invalid", owner, output.Status)
		}
	}
}

type stubTokenSource struct {
	token string
	err   error
}

func (source stubTokenSource) Token(context.Context, string, string) (string, error) {
	return source.token, source.err
}
```

Append to `internal/activities/template_run_test.go`:

```go
// A run's checkout needs the credential just as a registration's clone does;
// this is the path that feeds Terraform.
func TestFetchSourcePassesInstallationTokenToCheckout(t *testing.T) {
	t.Parallel()

	git := &recordingSourceGitRunner{}
	activities := &TemplateRunActivities{
		recorder:        &recordingStatusRecorder{},
		runRoot:         t.TempDir(),
		terraformRunner: &recordingTerraformRunner{},
		git:             git,
		tokens:          stubTokenSource{token: "ghs_minted"},
	}

	if _, err := activities.FetchSource(context.Background(), domain.FetchSourceActivityInput{
		WorkspacePath:     t.TempDir(),
		RepoOwner:         "acme",
		RepoName:          "private-infra",
		ResolvedCommitSHA: "a1b2c3d",
		RootPath:          "modules/vpc",
	}); err != nil {
		t.Fatalf("FetchSource returned error: %v", err)
	}

	if git.credential != gitrunner.NewGitCredential("ghs_minted") {
		t.Fatal("checkout did not receive the installation credential")
	}
}

func TestFetchSourceReportsUninstalledApp(t *testing.T) {
	t.Parallel()

	activities := &TemplateRunActivities{
		recorder:        &recordingStatusRecorder{},
		runRoot:         t.TempDir(),
		terraformRunner: &recordingTerraformRunner{},
		git:             &recordingSourceGitRunner{},
		tokens:          stubTokenSource{err: githubapp.ErrAppNotInstalled},
	}

	_, err := activities.FetchSource(context.Background(), domain.FetchSourceActivityInput{
		WorkspacePath:     t.TempDir(),
		RepoOwner:         "acme",
		RepoName:          "private-infra",
		ResolvedCommitSHA: "a1b2c3d",
		RootPath:          "modules/vpc",
	})
	if !errors.Is(err, githubapp.ErrAppNotInstalled) {
		t.Fatalf("error = %v, want ErrAppNotInstalled", err)
	}
	if !strings.Contains(err.Error(), "acme/private-infra") {
		t.Fatalf("error = %v, want it to name the repository", err)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/activities/ -v`
Expected: FAIL to compile — `undefined: WithTemplateSyncTokenSource`, `unknown field tokens`.

- [ ] **Step 3: Implement in `template_sync.go`**

Replace `publicGitHubRepoURL` and add the token source. New imports: `net/url` is not needed;
add `"github.com/vishu42/tflive/internal/githubapp"`.

```go
// GitHubTokenSource resolves a short-lived token granting read access to one
// repository.
//
// The interface lives here, on the consumer, so the activity packages depend on
// the capability rather than on githubapp's concrete client -- and so tests can
// supply a token without an HTTP server.
type GitHubTokenSource interface {
	Token(ctx context.Context, owner string, repo string) (string, error)
}
```

Add the field and option:

```go
type TemplateSyncActivities struct {
	store    TemplateSyncStore
	git      runner.GitRunner
	tempRoot string
	tokens   GitHubTokenSource
}

// WithTemplateSyncTokenSource authenticates source fetches against private
// repositories. Without it, only public repositories can be registered.
func WithTemplateSyncTokenSource(tokens GitHubTokenSource) TemplateSyncOption {
	return func(activities *TemplateSyncActivities) {
		activities.tokens = tokens
	}
}
```

Replace the URL helper:

```go
// gitHubRepoURL builds the clone URL for owner/repo.
//
// The identifiers are validated rather than trusted: they arrive from an API
// request, and a value carrying a slash, a query, a fragment, or whitespace
// would produce a URL that is not the repository the caller named. The
// authority is fixed before the first path separator, so no value can redirect
// the request to another host -- but a malformed one still deserves a clear
// rejection at the boundary instead of an obscure git failure.
func gitHubRepoURL(owner string, repo string) (string, error) {
	if err := validateRepoIdentifier("repository owner", owner); err != nil {
		return "", err
	}
	if err := validateRepoIdentifier("repository name", repo); err != nil {
		return "", err
	}
	return fmt.Sprintf("https://github.com/%s/%s.git", owner, repo), nil
}

func validateRepoIdentifier(field string, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", field)
	}
	if value != strings.TrimSpace(value) {
		return fmt.Errorf("%s %q must not have leading or trailing whitespace", field, value)
	}
	for _, character := range value {
		if character <= ' ' || character == '/' || character == '?' || character == '#' ||
			character == '@' || character == ':' || character == '\\' || character == 0x7f {
			return fmt.Errorf("%s %q contains an unsupported character", field, value)
		}
	}
	return nil
}

// repoCredential resolves the credential for one repository.
//
// It runs inside the activity, never in workflow code, so the token stays out
// of Temporal history -- the same boundary CredentialDecryptor already
// establishes for Terraform credentials. With no token source configured it
// returns the zero credential and the clone proceeds unauthenticated.
func repoCredential(ctx context.Context, tokens GitHubTokenSource, owner string, repo string) (runner.GitCredential, error) {
	if tokens == nil {
		return runner.GitCredential{}, nil
	}
	token, err := tokens.Token(ctx, owner, repo)
	if err != nil {
		return runner.GitCredential{}, err
	}
	return runner.NewGitCredential(token), nil
}
```

In `SyncTemplate`, replace the clone block:

```go
	repoURL, err := gitHubRepoURL(input.RepoOwner, input.RepoName)
	if err != nil {
		return invalidTemplateSyncOutput("%v", err), nil
	}
	credential, err := repoCredential(ctx, activities.tokens, input.RepoOwner, input.RepoName)
	if err != nil {
		if errors.Is(err, githubapp.ErrAppNotInstalled) {
			return invalidTemplateSyncOutput(
				"the tflive GitHub App is not installed on %s/%s; ask an organization admin to install it before registering this template",
				input.RepoOwner, input.RepoName,
			), nil
		}
		return domain.TemplateSyncActivityOutput{}, fmt.Errorf("resolve github credential: %w", err)
	}
	if err := activities.git.Clone(ctx, repoURL, input.SourceRef, repoPath, credential); err != nil {
		return invalidTemplateSyncOutput("clone repository %s/%s at %q: %v", input.RepoOwner, input.RepoName, input.SourceRef, err), nil
	}
```

- [ ] **Step 4: Implement in `template_run.go`**

Add the `tokens GitHubTokenSource` field to `TemplateRunActivities`, add it as a parameter to
`NewTemplateRunActivitiesWithCredentials` (before the variadic), and pass `nil` from the two
older constructors:

```go
func NewTemplateRunActivitiesWithLogStore(recorder StatusRecorder, runRoot string, logStore TemplateRunLogStore, terraformRunners ...TerraformRunner) *TemplateRunActivities {
	return NewTemplateRunActivitiesWithCredentials(recorder, runRoot, logStore, nil, nil, nil, terraformRunners...)
}

// NewTemplateRunActivitiesWithCredentials wires runtime credential lookup and
// decryption into activities, plus the GitHub token source used to fetch source
// from a private repository.
func NewTemplateRunActivitiesWithCredentials(recorder StatusRecorder, runRoot string, logStore TemplateRunLogStore, credentialReader CredentialReader, credentialDecryptor CredentialDecryptor, tokens GitHubTokenSource, terraformRunners ...TerraformRunner) *TemplateRunActivities {
```

with `tokens: tokens,` added to the returned struct literal.

Replace the fetch block in `FetchSource`:

```go
	repoURL, err := gitHubRepoURL(input.RepoOwner, input.RepoName)
	if err != nil {
		return domain.FetchSourceActivityOutput{}, err
	}
	credential, err := repoCredential(ctx, activities.tokens, input.RepoOwner, input.RepoName)
	if err != nil {
		return domain.FetchSourceActivityOutput{}, fmt.Errorf("resolve github credential for %s/%s: %w", input.RepoOwner, input.RepoName, err)
	}
	// The commit is what the revision means, so it is what runs. Checking out a
	// ref would let the source move between a plan and the apply that was
	// approved against it, and would ignore the revision entirely once an
	// upgrade pointed the component somewhere the installed ref never reached.
	//
	// The ref remains the fallback only for runs queued before the commit was
	// threaded through; those payloads have no commit to check out. Once such a
	// queue has drained the branch is dead, and with it the last path by which a
	// run resolves its own source.
	if commitSHA := strings.TrimSpace(input.ResolvedCommitSHA); commitSHA != "" {
		if err := git.CheckoutCommit(ctx, repoURL, commitSHA, sourcePath, credential); err != nil {
			return domain.FetchSourceActivityOutput{}, fmt.Errorf("checkout source commit %s: %w", commitSHA, err)
		}
	} else if err := git.Clone(ctx, repoURL, input.SourceRef, sourcePath, credential); err != nil {
		return domain.FetchSourceActivityOutput{}, fmt.Errorf("clone source: %w", err)
	}
```

- [ ] **Step 5: Run the suite**

Run: `go test ./internal/... -v`
Expected: PASS. `go build ./...` will still fail at `cmd/worker` — Task 8 fixes that.

- [ ] **Step 6: Commit**

```bash
git add internal/activities
git commit -m "feat: authenticate template source fetches with a GitHub App token"
```

---

## Task 8: Wire the worker, configuration files, and docs

**Files:**
- Modify: `cmd/worker/main.go:160,174,217-224`
- Modify: `.env.example`
- Modify: `docker-compose.app.yaml` (worker service env block, near line 70-83)
- Modify: `docs/architecture.md` (`## GitHub Integration`, ~line 533)

**Interfaces:**
- Consumes: everything from Tasks 4-7.
- Produces: a running worker that authenticates private repository fetches.

- [ ] **Step 1: Build the token source in the worker**

In `cmd/worker/main.go`, next to the existing `credentialCipher` block (~line 217):

```go
	var gitHubTokens *githubapp.TokenSource
	if cfg.GitHubApp.Enabled() {
		privateKey, err := githubapp.ParsePrivateKey(cfg.GitHubApp.PrivateKey.Value())
		if err != nil {
			// Unreachable in practice: LoadWorkerConfig parses the same key at
			// startup precisely so this cannot fail here.
			return fmt.Errorf("parse github app private key: %w", err)
		}
		gitHubTokens = githubapp.NewTokenSource(githubapp.NewClient(cfg.GitHubApp.AppID, privateKey))
	}
```

Add `"github.com/vishu42/tflive/internal/githubapp"` to the imports. Thread `gitHubTokens` down
to the two activity constructors, matching however `credentialCipher` is already threaded (a
struct field on the deps value, or a closure parameter — follow the existing shape rather than
inventing a new one).

At line 160:

```go
			templateRunActivities := activities.NewTemplateRunActivitiesWithCredentials(store, runRoot, logStore, reader, decryptor, gitHubTokens)
```

At line 174:

```go
			templateSyncActivities := activities.NewTemplateSyncActivities(store, activities.WithTemplateSyncTokenSource(gitHubTokens))
```

> A nil `*githubapp.TokenSource` stored in the `GitHubTokenSource` interface is a non-nil
> interface value, but `(*TokenSource).Token` has a nil receiver guard (Task 5) and returns an
> empty token, so the unconfigured path stays correct. Verify with Step 3's test rather than
> trusting this note.

- [ ] **Step 2: Add a regression test for the unconfigured worker path**

Append to `internal/githubapp/tokensource_test.go`:

```go
// The worker stores a nil *TokenSource in an interface when no App is
// configured. A nil receiver guard is what keeps that from panicking on the
// first clone of a public repository.
func TestNilTokenSourceThroughInterfaceIsSafe(t *testing.T) {
	t.Parallel()

	var source *TokenSource
	var iface interface {
		Token(ctx context.Context, owner string, repo string) (string, error)
	} = source

	token, err := iface.Token(context.Background(), "acme", "public")
	if err != nil {
		t.Fatalf("Token returned error: %v", err)
	}
	if token != "" {
		t.Fatalf("token = %q, want empty", token)
	}
}
```

- [ ] **Step 3: Run the full suite and build**

Run: `go build ./... && go vet ./... && go test ./... -race`
Expected: PASS everywhere, no vet findings.

- [ ] **Step 4: Update `.env.example`**

Add near `CREDENTIAL_ENCRYPTION_KEY`:

```bash
# GitHub App used to read private template repositories.
# Optional: leave both empty and only public repositories can be registered.
#
# Create an App with Repository permission "Contents: Read-only", install it on
# each organization whose repositories tflive should reach, then set the App's
# numeric id and its private key here. The key may be the PEM GitHub downloads
# or, more conveniently for a single-line value, that PEM base64-encoded:
#   base64 -i app-private-key.pem | tr -d '\n'
#
# To reach repositories in organizations other than the one that owns the App,
# the App must be created with "Where can this GitHub App be installed?" set to
# "Any account". That setting is awkward to change later.
GITHUB_APP_ID=
GITHUB_APP_PRIVATE_KEY=
```

- [ ] **Step 5: Update `docker-compose.app.yaml`**

In the **worker** service environment block only (the API process must not receive the key):

```yaml
      GITHUB_APP_ID: ${GITHUB_APP_ID:-}
      GITHUB_APP_PRIVATE_KEY: ${GITHUB_APP_PRIVATE_KEY:-}
```

- [ ] **Step 6: Update `docs/architecture.md`**

Under `## GitHub Integration`, after the `GitHubIntegration` block, add:

```markdown
As of the private-repository work (#239) no `GitHubIntegration` row exists yet. The worker holds
the App's id and private key in configuration and resolves the installation covering a repository
on demand via `GET /repos/{owner}/{repo}/installation`, then mints a repository-scoped read-only
token for the clone. Nothing is persisted, so no schema or lifecycle has to stay in sync with
GitHub, and one App installed on several organizations works without additional configuration.

The table above becomes real with the installation-lifecycle work, where it serves as a cache and
an allowlist in front of that same resolver, governed by the `github_integration` OpenFGA type
(#144). Until it exists, any principal who can publish a template can cause a clone from any
repository the App was installed on.

The installation token reaches git through `GIT_CONFIG_*` environment variables carrying an
`http.https://github.com/.extraheader` header, never through the clone URL or argv, so it is not
written into the run workspace's `.git/config` — the directory Terraform then executes from.

Not covered: repositories using git-LFS, and Terraform module sources of the form
`source = "git::https://github.com/org/private-module"`, which Terraform fetches itself without
the credential.
```

- [ ] **Step 7: Commit**

```bash
git add cmd/worker/main.go .env.example docker-compose.app.yaml docs/architecture.md internal/githubapp/tokensource_test.go
git commit -m "feat: wire the GitHub App token source into the worker"
```

---

## Manual End-to-End Verification

Automated tests cannot prove the header is accepted by real GitHub. Run this before closing #239.

- [ ] **1.** Create a GitHub App: Repository permission **Contents: Read-only**, webhooks
  inactive, installable on **Any account**. Generate a private key; note the App ID.
- [ ] **2.** Install it on a private test repo containing a small Terraform module and a
  `template.yaml`.
- [ ] **3.** Set `GITHUB_APP_ID` and `GITHUB_APP_PRIVATE_KEY` in `.env`; `docker compose up`.
- [ ] **4.** Register the private repo through the UI. Expect status `completed` with a resolved
  commit SHA.
- [ ] **5.** Add it to a stack and run a plan. Expect `FetchSource` to check out the commit and
  Terraform to plan successfully.
- [ ] **6. Leak audit — the one step not to skip.** With the token value from the worker logs (or
  a fresh mint), search everywhere it could have escaped:

```bash
docker compose exec worker sh -c 'grep -ri "ghs_" /var/lib/tflive/runs/ || echo CLEAN'
docker compose exec worker sh -c 'cat /var/lib/tflive/runs/*/*/source/.git/config'
docker compose exec postgres psql -U postgres -c "select error_summary from template_registrations where error_summary is not null;"
docker compose logs worker | grep -c "ghs_" || echo CLEAN
```

  Expect `CLEAN`, and a `.git/config` with no `extraheader` and no `url` line.

- [ ] **7. Negative path.** Register a repo the App is *not* installed on. Expect the registration
  to fail with the "GitHub App is not installed" summary naming the repo, and — check the
  Temporal UI — to make exactly one attempt, not four.
- [ ] **8. Multi-org.** Install the App on a second organization; register a private repo from it.
  Expect success with no configuration change.
- [ ] **9. Regressions.** Register a public repo with the App configured, then unset both env
  vars, restart the worker, and register a public repo again. Both must succeed.
- [ ] **10.** Long-wait apply: start an apply, wait past the token's `expires_at`, then approve.
  The apply must succeed — it runs from source already on disk and never re-contacts GitHub.

---

## Self-Review Notes

Checked against #239:

- **Spec coverage.** §1 → Tasks 4-5; §2 → Tasks 1-3; §3 → Task 7; §4 → Tasks 6, 8. "Out of
  scope" items appear only in the `docs/architecture.md` text of Task 8, as documentation.
- **Type consistency.** `GitCredential` is constructed via `NewGitCredential` in Tasks 2, 3, 7 and
  compared by value in tests (safe — it is a comparable single-field struct).
  `GitHubTokenSource.Token` returns `(string, error)` in every task; `githubapp.TokenSource.Token`
  matches that signature structurally, which is what lets the concrete type satisfy the
  consumer-side interface without importing it.
- **Deviation from the ticket, deliberate.** The ticket says an empty credential leaves the
  command set "unchanged". Task 2 also sets `GIT_TERMINAL_PROMPT`, `GIT_ASKPASS`, and the trace
  guards on *unauthenticated* invocations. Argv is unchanged; the environment gains guards that
  benefit public clones equally. Task 3's `TestLocalGitRunnerWithoutCredentialLeavesArgsUnchanged`
  pins the accurate version of that claim.
- **Known gap, accepted.** `TokenSource` has no single-flight guard, so N concurrent activities
  for the same cold repo mint N tokens. Harmless — each is valid, and the last write wins — and
  a `sync.Map`-plus-`singleflight` fix is not worth the complexity at this volume.
