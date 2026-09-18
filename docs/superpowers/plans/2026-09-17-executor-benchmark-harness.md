# Executor Benchmark Harness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development
> (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** Produce two defensible numbers — the resource cost of one Terraform run (CPU-seconds,
peak RSS, disk, wall-clock, per phase) and the number of concurrent runs one pinned executor
sustains before it degrades — by instrumenting the system, making its concurrency knobs
configurable, and driving it with a mock OpenTofu provider that never touches a cloud.

**Architecture:** Three layers, each useless without the one before it. First, measurement:
`os/exec` already owns the only subprocess boundary in the codebase, so capturing
`ProcessState.SysUsage()` there yields exact per-command CPU and peak RSS with no sampling. A
Prometheus registry is exposed from both binaries, and the Temporal SDK's own metrics handler is
wired so schedule-to-start latency and worker slot occupancy become visible. Second, control: the
executor's session and activity concurrency and the control-plane queue's worker pool move from Go
constants to configuration, so a sweep is an environment variable rather than a rebuild. Third,
load: a mock provider served from a filesystem mirror inside the executor image makes `tofu init`
resolve offline and gives every run a configurable work shape — duration, CPU burn, allocation,
state size, resource count — and `cmd/bench` drives runs through the real HTTP API against a local
git daemon.

**Tech Stack:** Go 1.25. `github.com/prometheus/client_golang` (already an indirect dependency via
OpenFGA — promoted to direct), `go.temporal.io/sdk/contrib/tally` + `github.com/uber-go/tally/v4`
for the Temporal metrics bridge, stdlib `net/http/pprof` and `syscall`. The mock provider lives in
its own Go module so `terraform-plugin-framework` never enters the main module's dependency graph.

**Spec:** No issue yet. Background and the rejected alternatives are in this plan's own "Decisions
already taken" section.

---

## TL;DR

Twelve tasks. Tasks 1–5 make the system measurable and tunable; 6–9 build a load source that never
calls a cloud; 10–11 drive and observe it; 12 produces the numbers.

| # | Task | Deliverable |
|---|---|---|
| 1 | Subprocess resource accounting | `CommandExecutor` returns `RunResult{CPUUser, CPUSys, MaxRSS, ExitCode}` from `Rusage` |
| 2 | `internal/observability` | Prometheus registry, `/metrics`, `/debug/pprof`, mounted on api; new executor HTTP server |
| 3 | Temporal SDK metrics | `MetricsHandler` on both clients — schedule-to-start latency, worker slots |
| 4 | Terraform command metrics | Per-command duration, CPU-seconds, peak RSS, active runs, workspace bytes |
| 5 | Concurrency knobs | Executor session/activity caps and queue worker pool become configuration |
| 6 | Mock OpenTofu provider | `tflivemock_workload` with duration, CPU, allocation and state-size knobs |
| 7 | Provider filesystem mirror | Provider baked into `Dockerfile.executor`; `tofu init` resolves offline |
| 8 | Configurable git base URL | `TFLIVE_GIT_BASE_URL` + a `git daemon` container; no run fetches from github.com |
| 9 | Bench fixture module | The `.tf` module the benchmark runs, served from the local git daemon |
| 10 | `cmd/bench` load harness | Logs in, provisions, fires runs on an arrival schedule, records timelines |
| 11 | Bench compose overlay | Prometheus, Grafana, cadvisor, node-exporter, pinned CPU and memory per service |
| 12 | Experiments and writeup | Seven experiments; `docs/benchmarking.md` |

**Ordering is deliberate.** Nothing downstream is interpretable until Tasks 1–4 land: without
instrumentation the benchmark reports wall-clock and nothing else, and without Task 5 the first
concurrency experiment measures how fast the box OOMs rather than how much work it does.

**The one rule everything else serves:** the instrumentation must not change what a run does.
Tasks 1–4 add measurement at existing seams and are behaviour-preserving. If a run's command
sequence, log contents or status transitions change, something went wrong.

**Expect one intentional build break:** after Task 1 widens the `CommandExecutor` interface,
`internal/runner` test fakes and both call sites must be updated in the same task. Anywhere else, a
broken build means a mistake.

---

## Decisions already taken

Recorded here so they are not relitigated mid-implementation.

**The 45-minute timeout and heartbeats are already done.** Commit `10d65d1` ("fix: bound Terraform
commands by a configurable timeout and heartbeat them") landed `DefaultTerraformTimeout = 45m`
configurable through `TFLIVE_TERRAFORM_TIMEOUT` (`internal/domain/workflow.go:57`,
`internal/config/config.go:155`), `TerraformHeartbeatInterval = 20s` /
`TerraformHeartbeatTimeout = 2m` (`internal/domain/workflow.go:74-75`), `startHeartbeat` in
`internal/activities/template_run.go:320`, and `HeartbeatTimeout` on the activity options at
`internal/workflows/template_run.go:440`. A 15-minute run works today. **No task in this plan
touches timeouts or heartbeats.**

**A mock that only sleeps measures the wrong thing.** A sleeping process costs roughly zero CPU, so
a sleep-only mock yields the session and memory ceiling, never the throughput ceiling. Task 6's
provider therefore takes work-*shape* knobs, and Task 12 sweeps idle-heavy against work-heavy
profiles. The answer is a surface, not a number.

**k6 is not the primary load tool.** 100 long-lived runs generate a few hundred POSTs over fifteen
minutes; k6 would sit idle. `cmd/bench` (Task 10) is the run driver. k6 appears once, in experiment
6, for read-path load *while* runs execute.

**`filesystem_mirror`, not `dev_overrides`.** `dev_overrides` does not satisfy `tofu init`, and
`init` is a phase being measured.

**The provider gets its own Go module.** `terraform-plugin-framework` pulls a large dependency tree.
Keeping it in `bench/provider/go.mod` means `go.mod` at the repository root is untouched by Task 6.

**A laptop cannot answer the scaling question.** Under Docker Desktop we would be measuring the VM.
Task 11 pins resources and Task 12 runs on a fixed Linux host whose spec is recorded with every
result.

---

## Global Constraints

- **Instrumentation must not alter run semantics.** Tasks 1–4 add measurement at existing seams.
  Command argv, log contents, artifact keys and status transitions are unchanged.
- **No new dependencies in the root module except two:** `github.com/uber-go/tally/v4` and
  `go.temporal.io/sdk/contrib/tally`. `github.com/prometheus/client_golang` is already present as
  indirect (`go.mod:29`) and is promoted to direct. Everything else is stdlib.
- **Go 1.25.0**, toolchain go1.25.14 (`go.mod`).
- **tflive is pre-production.** No users, disposable state. Never write a migration for backward
  compatibility, never keep a deprecated signature "just in case". Workflow changes need no
  Temporal versioning.
- **New knobs default to today's behaviour.** `EXECUTOR_MAX_CONCURRENT_SESSIONS` unset means the
  Temporal SDK default, exactly as now. Choosing a production default is a product decision and is
  explicitly **out of scope** — Task 12 produces the evidence for it, nothing in this plan picks it.
- **Nothing in `bench/` or `cmd/bench/` may be imported by `cmd/api` or `cmd/executor`.** The
  benchmark depends on the product; the product never depends on the benchmark.
- **`Maxrss` is kilobytes on Linux and bytes on macOS.** Normalise at the single point of capture in
  Task 1, with the platform difference documented there and nowhere else.
- Follow existing house style: `t.Parallel()`, `reflect.DeepEqual` against a `want` literal, errors
  wrapped with `%w`, doc comments that explain *why*.

---

## File Structure

**Created:**

| File | Responsibility |
|---|---|
| `internal/observability/registry.go` | Prometheus registry, Go + process collectors, `http.Handler` with `/metrics` and `/debug/pprof` |
| `internal/observability/registry_test.go` | Handler serves both paths; registry carries runtime collectors |
| `internal/observability/terraform.go` | The `tflive_terraform_*` and `tflive_executor_*` collectors and their recording helpers |
| `internal/observability/terraform_test.go` | Recording helpers produce the expected series and labels |
| `internal/observability/queue.go` | `tflive_queue_depth`, `tflive_queue_claim_latency_seconds` |
| `bench/provider/go.mod` | Separate module — keeps `terraform-plugin-framework` out of the root graph |
| `bench/provider/main.go` | Provider entrypoint |
| `bench/provider/workload_resource.go` | `tflivemock_workload` and its knobs |
| `bench/provider/workload_resource_test.go` | Knob semantics: durations honoured, payload sized, CPU burn bounded |
| `bench/fixtures/mock-workload/main.tf` | The module the benchmark runs |
| `bench/fixtures/mock-workload/variables.tf` | Variables mapped from stack template config via `TF_VAR_*` |
| `cmd/bench/main.go` | Harness entrypoint and flags |
| `cmd/bench/client.go` | Authenticated API client (cookie jar) |
| `cmd/bench/scenario.go` | Arrival patterns and the run lifecycle driver |
| `cmd/bench/report.go` | JSON Lines output and the summary table |
| `cmd/bench/*_test.go` | Client request shape, arrival scheduling, report aggregation |
| `deploy/bench/prometheus.yml` | Scrape config: api, executor, cadvisor, node-exporter, Temporal |
| `deploy/bench/grafana/` | Provisioned datasource and dashboard |
| `deploy/bench/bench.tofurc` | `provider_installation { filesystem_mirror { ... } }` |
| `docker-compose.bench.yaml` | Overlay: observability stack, git daemon, pinned resources |
| `docs/benchmarking.md` | How to run it, and the results |

**Modified:**

| File | Change |
|---|---|
| `internal/runner/executor.go` | `CommandExecutor` returns `RunResult`; capture `Rusage` |
| `internal/runner/terraform.go` | Thread `RunResult` out of `Run`, `run`, `selectWorkspace`, `runWithTerraformVariables` |
| `internal/runner/git.go` | Same seam, result discarded |
| `internal/runner/terraform_test.go`, `git_test.go` | `recordingCommandExecutor` returns `RunResult` |
| `internal/activities/template_run.go` | Emit command metrics; workspace bytes at completion; active-runs gauge |
| `internal/activities/template_sync.go` | `gitHubRepoURL` takes a configurable base |
| `internal/temporal/client.go` | `MetricsHandler` on `client.Options` |
| `internal/queue/controller.go` | Emit depth and claim latency |
| `internal/config/config.go` | New executor, queue and git-base settings |
| `internal/config/config_test.go` | Loader cases for each |
| `cmd/api/main.go` | Mount observability handler; pass queue options; `ReadTimeout`/`WriteTimeout` |
| `cmd/executor/main.go` | Observability HTTP server; worker concurrency options |
| `Dockerfile.executor` | Build and stage the mock provider mirror; `EXPOSE` the metrics port |
| `docker-compose.yaml` | Publish the executor metrics port |
| `.env.example` | Document every new variable |
| `docs/architecture.md` | Replace the speculative scaling section at `:794-828` with measured numbers |
| `go.mod`, `go.sum` | Two new direct dependencies |

---

## Task 1: Per-subprocess resource accounting

`internal/runner/executor.go:35` calls `cmd.Run()` and discards `cmd.ProcessState`. That struct
carries the kernel's own accounting for the process that just exited — exact user and system CPU
time and peak resident set size. Since the cost of a run lives in the `tofu` child and not in our
Go process, this is the only place the headline number can come from, and it costs one struct and
one type assertion.

**Files:**
- Modify: `internal/runner/executor.go`
- Modify: `internal/runner/terraform.go` (`Run`, `run`, `selectWorkspace`, `runWithTerraformVariables`)
- Modify: `internal/runner/git.go` (same seam; result discarded here)
- Test: `internal/runner/executor_test.go` (new), `internal/runner/terraform_test.go`, `internal/runner/git_test.go`

**Interfaces:**
- Consumes: nothing (first task).
- Produces: `runner.RunResult` and a `CommandExecutor` that returns it. Task 4 consumes both.

- [ ] **Step 1: Write the failing tests**

New `internal/runner/executor_test.go`, exercising the real `osExecCommandExecutor` against
`/bin/sh` so the assertion is about kernel accounting, not a fake:

```go
func TestOSExecCommandExecutorReportsResourceUsage(t *testing.T) {
	t.Parallel()

	result, err := osExecCommandExecutor{}.Run(
		context.Background(), t.TempDir(), nil, io.Discard, io.Discard,
		"/bin/sh", "-c", "i=0; while [ $i -lt 200000 ]; do i=$((i+1)); done",
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", result.ExitCode)
	}
	if result.CPUUser+result.CPUSys <= 0 {
		t.Errorf("CPU time = %v, want > 0", result.CPUUser+result.CPUSys)
	}
	if result.MaxRSS <= 0 {
		t.Errorf("MaxRSS = %d, want > 0", result.MaxRSS)
	}
}

func TestOSExecCommandExecutorReportsExitCodeOnFailure(t *testing.T) {
	t.Parallel()

	result, err := osExecCommandExecutor{}.Run(
		context.Background(), t.TempDir(), nil, io.Discard, io.Discard,
		"/bin/sh", "-c", "exit 3",
	)
	if err == nil {
		t.Fatal("Run() error = nil, want a non-zero exit")
	}
	if result.ExitCode != 3 {
		t.Errorf("ExitCode = %d, want 3", result.ExitCode)
	}
}
```

Then update `recordingCommandExecutor` in `internal/runner/terraform_test.go` and
`internal/runner/git_test.go` to return `(RunResult, error)`. The recorded-argv assertions are
unchanged; only the fake's signature moves.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/runner/ 2>&1 | head -20`
Expected: build failure — `osExecCommandExecutor.Run` returns one value, tests want two.

- [ ] **Step 3: Widen the seam**

In `internal/runner/executor.go`:

```go
// RunResult is the kernel's accounting for one finished subprocess.
//
// It is captured because the resource cost of a run lives in the OpenTofu
// child process, not in this one: our own Go runtime metrics would report a
// worker that is almost entirely idle while a `tofu apply` saturates a core.
// Rusage is filled in by wait(2), so these figures are exact rather than
// sampled, and they cost nothing beyond reading a struct the kernel already
// wrote.
//
// MaxRSS is normalised to bytes here. The kernel reports it in kilobytes on
// Linux and in bytes on Darwin, and no caller should have to know that.
type RunResult struct {
	CPUUser  time.Duration
	CPUSys   time.Duration
	MaxRSS   int64
	ExitCode int
}

type CommandExecutor interface {
	Run(ctx context.Context, dir string, env []string, stdout io.Writer, stderr io.Writer, name string, args ...string) (RunResult, error)
}

func (osExecCommandExecutor) Run(ctx context.Context, dir string, env []string, stdout io.Writer, stderr io.Writer, name string, args ...string) (RunResult, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	err := cmd.Run()
	return resultFrom(cmd.ProcessState), err
}

// resultFrom reads what the kernel recorded for a finished process.
//
// A nil ProcessState means the process never started -- a missing binary, a
// context already cancelled -- so there is nothing to account for and the zero
// value is correct. SysUsage is documented as *syscall.Rusage on Unix but the
// interface is deliberately open, so the assertion is checked rather than
// forced: an unexpected platform loses the resource figures instead of
// panicking in the middle of a run.
func resultFrom(state *os.ProcessState) RunResult {
	if state == nil {
		return RunResult{}
	}
	result := RunResult{
		CPUUser:  state.UserTime(),
		CPUSys:   state.SystemTime(),
		ExitCode: state.ExitCode(),
	}
	if usage, ok := state.SysUsage().(*syscall.Rusage); ok {
		result.MaxRSS = int64(usage.Maxrss) * maxRSSUnit
	}
	return result
}
```

Add `internal/runner/maxrss_linux.go` (`//go:build linux`) with `const maxRSSUnit = 1024` and
`internal/runner/maxrss_darwin.go` (`//go:build darwin`) with `const maxRSSUnit = 1`.

- [ ] **Step 4: Thread the result through the callers**

In `internal/runner/terraform.go`, change `run`, `selectWorkspace` and `runWithTerraformVariables`
to return `(RunResult, error)`, and `Run` likewise. `selectWorkspace` returns the result of
whichever invocation ultimately succeeded — if `workspace select` fails and `workspace new`
succeeds, the caller accounts for both, so sum them:

```go
func (runner *LocalProcessRunner) selectWorkspace(ctx context.Context, input TerraformCommand) (RunResult, error) {
	stdout, stderr := outputWriters(input)
	selected, err := runner.executor.Run(/* ... workspace select ... */)
	if err == nil {
		return selected, nil
	}
	created, err := runner.executor.Run(/* ... workspace new ... */)
	total := sumResults(selected, created)
	if err != nil {
		return total, &CommandError{Command: domain.TerraformCommandSelectWorkspace, Err: fmt.Errorf("select or new: %w", err)}
	}
	return total, nil
}
```

`sumResults` adds CPU times and takes the maximum RSS, with the exit code of the last invocation.

In `internal/runner/git.go`, the seam changes but git resource usage is not measured: assign the
result to `_` at each call site and leave a one-line comment saying why.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/runner/ -v && go build ./...`
Expected: PASS, build clean. `internal/activities` still compiles — it calls
`LocalProcessRunner.Run` and now discards a second return value until Task 4.

- [ ] **Step 6: Commit**

```bash
git add internal/runner/
git commit -m "feat: report per-subprocess cpu and peak rss from the command executor"
```

---

## Task 2: The observability package and its endpoints

Nothing in the tree exposes a metric today, and the executor runs no HTTP server at all — under
load there is no way to tell a wedged executor from an idle one. This task creates the registry and
gives both binaries somewhere to serve it from.

**Files:**
- Create: `internal/observability/registry.go`, `internal/observability/registry_test.go`
- Modify: `cmd/api/main.go`, `cmd/executor/main.go`, `internal/config/config.go`,
  `internal/config/config_test.go`, `Dockerfile.executor`, `docker-compose.yaml`, `.env.example`
- Modify: `go.mod` (promote `prometheus/client_golang` to direct)

**Interfaces:**
- Consumes: nothing.
- Produces: `observability.Registry` (wrapping `*prometheus.Registry`) and `Registry.Handler()`.
  Tasks 3, 4 and 5 register collectors on it.

- [ ] **Step 1: Write the failing test**

`internal/observability/registry_test.go`: assert the handler serves `/metrics` with Go runtime
series present, and that `/debug/pprof/` responds. Keep it to those two facts — this is plumbing.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/observability/`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Implement the registry**

`New()` returns a registry with `collectors.NewGoCollector()` and
`collectors.NewProcessCollector(...)`. `Handler()` returns an `http.ServeMux` with
`promhttp.HandlerFor(registry, ...)` on `/metrics` and the stdlib pprof handlers on
`/debug/pprof/`. Do **not** import `net/http/pprof` for its `init()` side effect on
`DefaultServeMux` — register the four handlers explicitly, so nothing is exposed by accident.

- [ ] **Step 4: Mount it on the api**

In `cmd/api/main.go`, build the registry, and mount `Handler()` on the existing server alongside
the API routes. While here, set `ReadTimeout`, `WriteTimeout` and `IdleTimeout` on the
`http.Server` at `cmd/api/main.go:548` — it currently sets none, which a load test will find.

- [ ] **Step 5: Give the executor an HTTP server**

Add `EXECUTOR_HTTP_ADDRESS` (default `:8090`) to `LoadExecutorConfig` in
`internal/config/config.go:112`, with a loader test. In `cmd/executor/main.go`, start an
`http.Server` on it serving `Handler()` plus a real `/healthz`, and shut it down when the worker
stops. `EXPOSE 8090` in `Dockerfile.executor`; publish it in `docker-compose.yaml`.

Note that the executor's `/healthz` should report whether the Temporal worker is running, not a
static literal — the api's `/healthz` at `internal/api/server.go:332` is a static `{"status":"ok"}`
and is not a model to copy.

- [ ] **Step 6: Run tests and verify by hand**

```bash
go test ./internal/observability/ ./internal/config/ && go build ./...
docker compose up -d --wait
curl -s localhost:8081/metrics | head -5
curl -s localhost:8090/metrics | head -5
```
Expected: both return `go_*` and `process_*` series.

- [ ] **Step 7: Commit**

```bash
git add internal/observability/ cmd/ internal/config/ Dockerfile.executor docker-compose.yaml .env.example go.mod go.sum
git commit -m "feat: expose prometheus metrics and pprof from the api and the executor"
```

---

## Task 3: Temporal SDK metrics

`internal/temporal/client.go:38-41` builds `client.Options{HostPort, Namespace}` and leaves
`MetricsHandler` unset, so the SDK runs with a no-op handler. This one field is where
`temporal_activity_schedule_to_start_latency` and `temporal_worker_task_slots_available` come
from — respectively the clearest saturation signal in the system and the direct readout of whether
the executor is at its cap. It is the single highest-value line in the plan.

**Files:**
- Modify: `internal/temporal/client.go`, `internal/temporal/client_test.go`
- Modify: `cmd/api/main.go`, `cmd/executor/main.go`, `go.mod`

- [ ] **Step 1: Write the failing test**

Extend `internal/temporal/client_test.go` to assert that `Config` carries an optional metrics
handler and that it reaches `client.Options`. The existing tests there only cover config validation
and context cancellation before dial; follow that shape rather than dialling a server.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/temporal/`

- [ ] **Step 3: Wire tally to Prometheus**

Add an optional `MetricsHandler client.MetricsHandler` to `temporal.Config` and pass it through.
Build it from the observability registry with `tally.NewRootScope` over a
`prometheus.NewReporter` on our registry, wrapped by `sdktally.NewMetricsHandler`. Pass it from
both `cmd/api/main.go` and `cmd/executor/main.go`. A nil handler must behave exactly as today.

- [ ] **Step 4: Verify by hand**

```bash
docker compose up -d --wait
# start one run through the UI or the API, then:
curl -s localhost:8090/metrics | grep -c temporal_
```
Expected: non-zero, including `temporal_activity_schedule_to_start_latency` after the run starts.

- [ ] **Step 5: Commit**

```bash
git add internal/temporal/ cmd/ go.mod go.sum
git commit -m "feat: report temporal sdk metrics from both workers"
```

---

## Task 4: Terraform command metrics

Task 1 captures the numbers; this task publishes them. `localTerraformRunner.RunTerraform`
(`internal/activities/template_run.go:265`) already brackets the subprocess with a heartbeat start
and stop, which is exactly where timing and accounting belong.

**Files:**
- Create: `internal/observability/terraform.go`, `internal/observability/terraform_test.go`
- Modify: `internal/activities/template_run.go`, and its tests

**Interfaces:**
- Consumes: `runner.RunResult` (Task 1), `observability.Registry` (Task 2).
- Produces: the metrics Task 12 reads.

| Metric | Type | Labels |
|---|---|---|
| `tflive_terraform_command_duration_seconds` | histogram | `command` |
| `tflive_terraform_command_cpu_seconds_total` | counter | `command`, `mode` (`user`/`sys`) |
| `tflive_terraform_command_max_rss_bytes` | histogram | `command` |
| `tflive_terraform_command_total` | counter | `command`, `outcome` (`success`/`failure`) |
| `tflive_executor_active_runs` | gauge | — |
| `tflive_run_workspace_bytes` | histogram | — |

- [ ] **Step 1: Write the failing tests**

In `internal/observability/terraform_test.go`, assert each recording helper produces the expected
series with the expected labels, using `prometheus/client_golang/prometheus/testutil`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/observability/`

- [ ] **Step 3: Implement the collectors and record from the activity**

Histogram buckets matter here and should be chosen deliberately, not left at the client default,
which tops out around ten seconds: durations span `workspace select` (sub-second) to a fifteen-minute
apply, and RSS spans tens of megabytes to gigabytes. Use explicit exponential buckets and say why in
a comment.

Give `localTerraformRunner` an optional metrics recorder field, defaulting to a no-op so the
existing direct-construction tests are unaffected, and wire the real one through
`NewTemplateRunActivities` (`internal/activities/template_run.go:75`) from `cmd/executor/main.go`.
Increment the active-runs gauge in `PrepareWorkspace` and decrement in `ReleaseRunKey`, so the
gauge tracks session occupancy including the approval wait — which is precisely the occupancy
Task 12 experiment 2 is looking for.

Measure workspace bytes with a `filepath.WalkDir` over the run workspace at `ReleaseRunKey`. This
is the only visibility into a real defect: run workspaces are never deleted — the sole
`os.RemoveAll` on a workspace is the control-plane sync path at
`internal/activities/template_sync.go:98` — and there is no provider plugin cache, so every run
downloads its full provider set into a per-run `.terraform/`. Task 7's local mirror makes both
invisible to the benchmark, so this metric plus the Task 12 caveat is what keeps them on the record.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/observability/ ./internal/activities/ && go build ./...`

- [ ] **Step 5: Verify against ground truth**

Start one run. Compare `tflive_terraform_command_cpu_seconds_total` and
`tflive_terraform_command_max_rss_bytes` against `docker stats` and, on the executor container,
`/usr/bin/time -v tofu plan` in the same workspace. They should be close. If they are not, the
accounting is wrong and every later experiment is invalid — stop and fix it here.

- [ ] **Step 6: Commit**

```bash
git add internal/observability/ internal/activities/ cmd/executor/
git commit -m "feat: record per-command terraform cpu, memory and duration"
```

---

## Task 5: Concurrency knobs

Two hardcoded values decide the shape of every scaling result, and neither can be changed without a
rebuild.

`cmd/executor/main.go:107` passes `temporalworker.Options{EnableSessionWorker: true}` and nothing
else, leaving `MaxConcurrentActivityExecutionSize` and `MaxConcurrentSessionExecutionSize` at the
SDK default of 1000. A single executor will accept a thousand sessions and fork a thousand `tofu`
processes; `deploy: replicas: 2` in compose is not a bound. `docs/architecture.md:814` already says
this out loud.

`cmd/api/main.go:252` constructs the queue controller with `queue.Options{}`, so
`defaultWorkers = 4` (`internal/queue/controller.go:19`) applies and `BatchSize` defaults to the
same. Workflow starts drain four at a time on a one-second poll. Expect this to be the first wall
under load, and expect it to be mistaken for an executor limit unless it is measurable.

**Files:**
- Modify: `internal/config/config.go`, `internal/config/config_test.go`
- Modify: `cmd/executor/main.go`, `cmd/api/main.go`
- Modify: `internal/queue/controller.go`
- Create: `internal/observability/queue.go`

| Env var | Replaces |
|---|---|
| `EXECUTOR_MAX_CONCURRENT_SESSIONS` | SDK default 1000 |
| `EXECUTOR_MAX_CONCURRENT_ACTIVITIES` | SDK default 1000 |
| `QUEUE_WORKERS` | `defaultWorkers = 4` |
| `QUEUE_BATCH_SIZE` | defaults to `Workers` |
| `QUEUE_POLL_INTERVAL` | `defaultPollInterval = 1s` |
| `DATABASE_MAX_CONNS` | pgx default — `cmd/api/main.go:194` uses a bare `pgxpool.New` |

- [ ] **Step 1: Write the failing loader tests**

In `internal/config/config_test.go`, one case per variable: unset means zero (today's behaviour),
a valid value parses, an invalid value returns `ErrInvalidConfig`. Follow the existing
`loadTerraformTimeout` test shape.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/config/`

- [ ] **Step 3: Implement**

Add the fields and loaders. Zero means "leave the SDK or package default alone" — this keeps every
default behaviour identical, which matters because choosing a production concurrency cap is out of
scope for this plan.

For `DATABASE_MAX_CONNS`, use `pgxpool.ParseConfig` and set `MaxConns` rather than smuggling
`pool_max_conns` into the DSN.

- [ ] **Step 4: Add queue metrics**

`internal/observability/queue.go` defines `tflive_queue_depth{kind}` and
`tflive_queue_claim_latency_seconds{kind}`; record them in `internal/queue/controller.go` around
the claim and deliver paths. The controller takes an optional recorder, nil-safe, so its existing
tests are untouched.

Do not perturb the invariant documented at `internal/queue/controller.go:28-40` —
`ItemTimeout × ceil(BatchSize/Workers) < Lease`. `withDefaults` already clamps and logs a violation
(`:104`); a sweep that trips it must keep doing so rather than becoming silently unsafe.

- [ ] **Step 5: Verify the knob bites**

```bash
EXECUTOR_MAX_CONCURRENT_SESSIONS=1 docker compose up -d --force-recreate executor
# fire two runs
```
Expected: the second run's session waits. `temporal_worker_task_slots_available` (Task 3) reads 0
while the first holds the slot.

- [ ] **Step 6: Commit**

```bash
git add internal/config/ internal/queue/ internal/observability/ cmd/ .env.example
git commit -m "feat: make executor and queue concurrency configurable"
```

---

## Task 6: The mock OpenTofu provider

**Files:**
- Create: `bench/provider/go.mod`, `bench/provider/main.go`,
  `bench/provider/workload_resource.go`, `bench/provider/workload_resource_test.go`

A resource whose cost is declared rather than incurred against a cloud. The knobs shape the work,
not just its length, because a sleeping process costs no CPU and would report the wrong ceiling.

| Attribute | Effect |
|---|---|
| `plan_duration` | sleep in `Read` / `PlanResourceChange` |
| `apply_duration` | sleep in `Create` / `Update` |
| `cpu_burn_ms` | busy loop — separates the CPU ceiling from the session ceiling |
| `alloc_bytes` | transient allocation during apply |
| `payload_bytes` | bytes written into state — drives `tofu`'s own RSS and state-file I/O |

With `count` in the fixture module, resource-graph width is a sixth knob.

- [ ] **Step 1: Initialise the module**

```bash
cd bench/provider && go mod init github.com/vishu42/tflive/bench/provider
go get github.com/hashicorp/terraform-plugin-framework
```

Confirm the root `go.mod` is unchanged afterwards — that is the whole point of the separate module.

- [ ] **Step 2: Write the failing tests**

Unit-test the knob semantics directly against the resource's logic, without a running provider:
`apply_duration` is honoured within tolerance, `payload_bytes` produces state of the requested size,
`cpu_burn_ms` consumes CPU rather than sleeping (assert `runtime`-measured CPU, not wall-clock).

- [ ] **Step 3: Implement**

Protocol 6 via terraform-plugin-framework; OpenTofu 1.12 supports it. Provider address
`registry.opentofu.org/tflive/tflivemock`, resource type `tflivemock_workload`. All durations
accept Go duration strings. Every sleep must honour the request context so a cancelled run kills
the provider promptly.

- [ ] **Step 4: Run tests and build**

```bash
cd bench/provider && go test ./... && go build ./...
cd ../.. && go build ./... && git diff --exit-code go.mod
```
Expected: PASS; root `go.mod` unchanged.

- [ ] **Step 5: Commit**

```bash
git add bench/provider/
git commit -m "feat: add a mock opentofu provider with configurable work shape"
```

---

## Task 7: Serve the provider from a filesystem mirror

Every run today runs `tofu init` against `registry.opentofu.org` with no plugin cache, downloading
its full provider set into a per-run `.terraform/`. For the benchmark that is unmetered network
variance we do not control; a local mirror makes `init` offline and deterministic.

**Files:**
- Create: `deploy/bench/bench.tofurc`
- Modify: `Dockerfile.executor`, `docker-compose.bench.yaml` (created in Task 11 — add the variable
  there, or to `docker-compose.yaml` under the bench profile)

- [ ] **Step 1: Stage the provider into the executor image**

Add a build stage that compiles `bench/provider` and copies the binary to the mirror layout:

```
/opt/tf-mirror/registry.opentofu.org/tflive/tflivemock/0.1.0/linux_${TARGETARCH}/terraform-provider-tflivemock_v0.1.0
```

`Dockerfile.executor` already takes `ARG TARGETARCH` for the OpenTofu download at `:11-22`; reuse it.

- [ ] **Step 2: Add the CLI config**

`deploy/bench/bench.tofurc`:

```hcl
provider_installation {
  filesystem_mirror {
    path    = "/opt/tf-mirror"
    include = ["registry.opentofu.org/tflive/*"]
  }
  direct {
    exclude = ["registry.opentofu.org/tflive/*"]
  }
}
```

Select it with `TF_CLI_CONFIG_FILE=/etc/tofu/bench.tofurc` in the environment of the executor
service. **No code change is needed:** `internal/runner/executor.go:30-36` inherits
`os.Environ()` into every subprocess whether or not credentials are appended.

Use `filesystem_mirror`, not `dev_overrides` — `dev_overrides` does not satisfy `init`, and `init`
is a phase being measured.

- [ ] **Step 3: Verify offline resolution**

```bash
docker compose build executor
docker compose run --rm --network none \
  -e TF_CLI_CONFIG_FILE=/etc/tofu/bench.tofurc \
  executor sh -c 'cd /tmp/fixture && tofu init'
```
Expected: succeeds **with networking disabled**. If it reaches out, the mirror is wrong.

- [ ] **Step 4: Commit**

```bash
git add Dockerfile.executor deploy/bench/
git commit -m "feat: serve the mock provider from a filesystem mirror in the executor image"
```

---

## Task 8: Configurable git base URL

`internal/activities/template_sync.go:192-199` hardcodes `https://github.com/%s/%s.git`, and the
API accepts only `repo_owner` and `repo_name` (`internal/api/server.go:824-829`). There is no
supported way to point a template at a local repository, so 100 concurrent runs means 100 live
GitHub fetches per iteration: rate limits and WAN variance in the middle of the measurement. This
is also a real product gap — GitHub Enterprise Server.

**Files:**
- Modify: `internal/activities/template_sync.go`, `internal/config/config.go`,
  `internal/config/config_test.go`, `cmd/api/main.go`, `cmd/executor/main.go`, `.env.example`

- [ ] **Step 1: Write the failing tests**

`gitHubRepoURL` with a base of `https://github.com` returns today's URL exactly; with
`git://gitserver` returns `git://gitserver/owner/repo.git`; `validateRepoIdentifier` still rejects
a slash, query, fragment or whitespace in either identifier.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/activities/ -run RepoURL`

- [ ] **Step 3: Implement**

Add `TFLIVE_GIT_BASE_URL`, defaulting to `https://github.com`. Thread it into the activities
constructors. **Keep `validateRepoIdentifier` exactly as it is** — the doc comment at
`internal/activities/template_sync.go:185-191` explains that the authority is fixed before the
first path separator so no user-supplied value can redirect the request to another host. That
property must survive: the base comes from operator configuration, the identifiers still come from
a request and are still validated.

- [ ] **Step 4: Open question to resolve here**

Verify that `SealSourceToken` (`internal/activities/control.go:79`) degrades cleanly when the
source is an anonymous local repository and no GitHub App is configured. If it hard-fails, add a
no-credential path — a local git daemon needs no token. Do not proceed to Task 9 until a run can
fetch from a non-GitHub source end to end.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/activities/ ./internal/config/ && go build ./...`

- [ ] **Step 6: Commit**

```bash
git add internal/activities/ internal/config/ cmd/ .env.example
git commit -m "feat: make the git base url configurable"
```

---

## Task 9: The bench fixture module

**Files:**
- Create: `bench/fixtures/mock-workload/main.tf`, `bench/fixtures/mock-workload/variables.tf`
- Modify: `docker-compose.bench.yaml` (git daemon service — may land with Task 11)

- [ ] **Step 1: Write the module**

`main.tf` declares `required_providers` pointing at `registry.opentofu.org/tflive/tflivemock` and
a `count`-ed `tflivemock_workload`. `variables.tf` declares one variable per knob plus
`resource_count`.

Values reach the run as `TF_VAR_*` through `terraformVariableEnv`
(`internal/runner/terraform.go:135-191`), which means a run's entire work shape is set from stack
template config through the API — no rebuild, no redeploy, one `PATCH .../config` per sweep point.

- [ ] **Step 2: Serve it over git**

Add a `git daemon --base-path=/srv/git --export-all --reuseaddr` container to the bench overlay,
with the fixture directory initialised as a bare repository. Set `TFLIVE_GIT_BASE_URL=git://gitserver`
on api and executor.

- [ ] **Step 3: Verify end to end**

Register a template revision through the API pointing at the fixture, then start a plan run.
Confirm it completes, and confirm `FetchSource` never reaches github.com by blocking egress from
the executor container for the duration.

- [ ] **Step 4: Commit**

```bash
git add bench/fixtures/ docker-compose.bench.yaml
git commit -m "feat: add the benchmark fixture module and a local git source"
```

---

## Task 10: The `cmd/bench` load harness

**Files:**
- Create: `cmd/bench/main.go`, `client.go`, `scenario.go`, `report.go`, and their tests

**Auth is simple and needs no new API surface.** `POST /v1/auth/login` as root with
`Content-Type: application/json` and **no** `Origin` or `Sec-Fetch-Site` header —
`internal/api/local_login.go:71-88` treats the absence of both as a non-browser client and passes
the CSRF check deliberately. The response is 204 with a `tflive_session` cookie; a
`net/http/cookiejar` carries it from there. There are no API tokens
(`internal/authn/middleware.go:16-28` is explicit that the cookie is the only credential).

**Routes the harness drives**, all under `/v1/tenants/{tenant_id}` unless noted, line numbers in
`internal/api/server.go`:

| Step | Route | Line |
|---|---|---|
| Login | `POST /v1/auth/login` | 161 |
| Register template | `POST …/template-revisions` | 174 |
| Poll registration (202) | `GET …/template-registrations/{id}` | 178 |
| Create stack | `POST …/stacks` | 184 |
| Install template | `POST …/stacks/{id}/templates` | 193 |
| Set config | `PATCH …/stack-templates/{id}/config` | 195 |
| Start run (201) | `POST …/stack-templates/{id}/runs` | 201 |
| Poll run | `GET …/template-runs/{id}` | 209 |
| Approve | `POST …/template-runs/{id}/approval` | 231 |
| Queue depth | `GET …/queue` | 221 |

- [ ] **Step 1: Write the failing tests**

Against `httptest`: the login request carries no `Origin` and a JSON content type; the cookie is
reused on subsequent calls; arrival patterns emit the right number of starts at the right offsets;
the reporter aggregates per-phase durations correctly.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/bench/`

- [ ] **Step 3: Implement**

Reuse `internal/domain` request and response types rather than redeclaring them. Arrival patterns:
`all-at-once`, `ramp`, `steady-rate`.

**An apply cannot be fired directly.** `internal/app/service.go:731-735` rejects an apply unless
`PlanState() == domain.PlanMatches`, so the unit is plan → poll to `plan_finished` → approve →
poll to terminal. That is also the more realistic shape: the session is held across the approval
wait (`internal/workflows/template_run.go:302`), and that occupancy is part of what is being
measured.

Record the per-run timeline from the status transitions already persisted by
`RecordTemplateRunStatus` (`internal/activities/control.go:41`) plus client-side timestamps. Emit
JSON Lines and a summary table.

Scenarios:

| Scenario | Load | Answers |
|---|---|---|
| `plan-only` | short runs, high arrival rate | control-plane throughput |
| `full-apply` | plan → approve → 15-minute apply | the headline capacity number |
| `sustained` | steady arrival for one hour | leaks, unbounded disk, session exhaustion |

- [ ] **Step 4: Verify against the product**

Run the `plan-only` scenario with five runs. The per-run timings in the result file must match what
the UI shows for the same run IDs. If they disagree, the harness is measuring something else.

- [ ] **Step 5: Commit**

```bash
git add cmd/bench/
git commit -m "feat: add the benchmark load harness"
```

---

## Task 11: The bench environment

**Files:**
- Create: `docker-compose.bench.yaml`, `deploy/bench/prometheus.yml`, `deploy/bench/grafana/`

- [ ] **Step 1: Compose the observability stack**

Prometheus scraping api (`:8081`), executor (`:8090`), cadvisor, node-exporter and Temporal.
Grafana with a provisioned datasource and one dashboard: schedule-to-start latency, active runs,
per-command CPU and RSS, queue depth, container CPU and RSS per executor, Postgres connections.

Two scrape targets need enabling: Temporal server metrics via `PROMETHEUS_ENDPOINT` on
`temporalio/auto-setup`, and Keycloak's management port 9000 — `KC_METRICS_ENABLED=true` is already
set at `docker-compose.yaml:53-54` but the port is not published.

- [ ] **Step 2: Pin resources**

Explicit `cpus:` and `mem_limit:` on every service. The executor gets a deliberately small
allocation — start at 2 CPU and 4 GB — so its knee appears at a concurrency the harness can reach.
Postgres, Temporal and the api get generous allocations so they are provably not the limit, and
Task 12 confirms that from their own metrics rather than assuming it.

Note that `deploy.resources.limits` is ignored outside swarm; use the service-level `cpus:` and
`mem_limit:` keys, which Compose V2 honours.

- [ ] **Step 3: Verify the accounting cross-check**

Bring the stack up, run the `plan-only` scenario, and compare cadvisor's container CPU and RSS for
the executor against the sum of `tflive_terraform_command_*` over the same window. They must agree
within a sane margin. **If they diverge badly, the Task 1 accounting is wrong and every experiment
result is invalid** — stop here.

- [ ] **Step 4: Commit**

```bash
git add docker-compose.bench.yaml deploy/bench/
git commit -m "feat: add the benchmark environment and dashboards"
```

---

## Task 12: Experiments and writeup

**Files:**
- Create: `docs/benchmarking.md`
- Modify: `docs/architecture.md` (replace the speculative scaling section at `:794-828`)

Run on a fixed Linux VM, not a laptop. Record the host spec with every result set.

| # | Experiment | Output |
|---|---|---|
| 1 | Single run, isolated, varying workload profile | Cost-per-run baseline: CPU-seconds, peak RSS, disk, phase breakdown |
| 2 | Concurrency sweep on **one** pinned executor (1, 2, 4, 8, 16, 32) | The knee, where p95 schedule-to-start latency or failure rate breaks. **This is the capacity number.** |
| 3 | Fixed concurrency, varying `EXECUTOR_MAX_CONCURRENT_SESSIONS` | Whether the binding constraint is CPU, RSS or the session cap |
| 4 | Horizontal sweep: 1 → 2 → 4 → 8 executors at proportional load | Whether throughput scales linearly, and where the control plane becomes the ceiling |
| 5 | Plan-only runs at high arrival rate, sweeping `QUEUE_WORKERS` | Run-starts per second; confirms whether four workers is the real wall |
| 6 | k6 against the read path (list runs, tail logs) while experiment 2 runs at its knee | Whether reads degrade under run load |
| 7 | One-hour sustained soak | Leaks, unbounded workspace growth, session exhaustion |

- [ ] **Step 1: Run experiments 1–7, each twice**

The knee must reproduce within roughly 10%. If it does not, the environment is not isolated enough
to publish numbers from — fix that before writing anything down.

- [ ] **Step 2: Write `docs/benchmarking.md`**

It must state, explicitly:

- The host specification and the exact workload profile for every figure.
- The derived runs-per-executor figure, normalised per core and per GB, and the executor count it
  implies for a target concurrency.
- **The caveats.** A local provider mirror removes provider-download cost, and run workspaces are
  never reclaimed. Real-world disk consumption and `init` duration will therefore be materially
  worse than these numbers. Cite `tflive_run_workspace_bytes` (Task 4) for the measured growth and
  name both defects as open issues rather than letting a clean benchmark hide them.
- One paragraph per experiment where the result contradicted the expectation.

- [ ] **Step 3: Update the architecture document**

`docs/architecture.md:794-828` currently predicts this work — it notes the unset
`MaxConcurrentSessionExecutionSize` and suggests task-queue backlog, schedule-to-start latency and
active Terraform activity count as HPA signals. Replace the speculation with the measured numbers
and the metric names that now exist.

- [ ] **Step 4: Commit**

```bash
git add docs/
git commit -m "docs: record executor capacity and per-run cost measurements"
```

---

## Manual End-to-End Verification

Run after Task 11, before Task 12.

1. `go test ./... && gofmt -l $(rg --files cmd internal -g '*.go')` — clean.
2. `make differential-test` (with `docker compose up -d postgres` first) — passes. Nothing here
   touches `internal/authorization`, but it is the repository's stated guard on the copied OpenFGA
   write path.
3. `curl -s localhost:8081/metrics` and `curl -s localhost:8090/metrics` — both return `go_*`,
   `process_*` and `temporal_*` series.
4. One real run through the UI: `tflive_terraform_command_cpu_seconds_total` and
   `tflive_terraform_command_max_rss_bytes` appear with per-command labels and plausible values,
   cross-checked against `docker stats`.
5. `EXECUTOR_MAX_CONCURRENT_SESSIONS=1`, two runs fired: the second waits, and
   `temporal_worker_task_slots_available` reads 0 while the first holds the slot.
6. `tofu init` in the fixture directory on the executor container succeeds **with `--network none`**.
7. A full run against the local git daemon with egress from the executor blocked — completes, and
   nothing reaches github.com.
8. `cmd/bench` `plan-only` with five runs: result-file timings match the UI for the same run IDs.
9. Cadvisor executor CPU and RSS agree with the sum of the per-command metrics over the same window.

Steps 4 and 9 are the load-bearing ones. They are the only checks that prove the numbers mean what
the writeup will claim they mean.

---

## Out of scope

- **Choosing a production concurrency default.** Task 12 produces the evidence; the decision is
  separate. Every knob added here defaults to today's behaviour.
- **Fixing the two defects the benchmark will expose** — no provider plugin cache, and run
  workspaces never reclaimed. Task 4 measures them and Task 12 records them. Fixing them changes
  the thing being measured and belongs in its own plan.
- **Kubernetes.** `deploy/` holds only `nginx.conf` and a Postgres init script. Task 11 pins
  resources in Compose on a single host, which is enough to answer the capacity question. Manifests
  and an HPA follow from the numbers, not before them.
- **Reducing the ~23 activities and ~12 Postgres writes per plan**, including the re-decryption of
  credentials on every command (`internal/workflows/template_run.go:388-400`). Experiment 5
  measures the cost; optimising it is separate work.
