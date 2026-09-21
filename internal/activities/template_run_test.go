package activities

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vishu42/tflive/internal/domain"
	"github.com/vishu42/tflive/internal/githubapp"
	gitrunner "github.com/vishu42/tflive/internal/runner"
	"github.com/vishu42/tflive/internal/runseal"
	"go.temporal.io/sdk/temporal"
)

func TestRecordTemplateRunStatusDelegatesToRecorder(t *testing.T) {
	t.Parallel()

	recorder := &recordingStatusRecorder{}
	activities := NewControlActivities(&controlStoreStub{status: recorder}, nil)
	input := domain.TemplateRunStatusActivityInput{
		RunID:           domain.TemplateRunID("run_123"),
		TenantID:        domain.TenantID("tenant_123"),
		StackTemplateID: domain.StackTemplateID("stack_template_123"),
		Operation:       domain.OperationPlan,
		Status:          domain.TemplateRunPlanFinished,
	}

	if err := activities.RecordTemplateRunStatus(context.Background(), input); err != nil {
		t.Fatalf("RecordTemplateRunStatus returned error: %v", err)
	}

	if !reflect.DeepEqual(recorder.input, input) {
		t.Fatalf("recorded input = %#v, want %#v", recorder.input, input)
	}
}

func TestRecordTemplateRunStatusWrapsRecorderError(t *testing.T) {
	t.Parallel()

	recorderErr := errors.New("database unavailable")
	activities := NewControlActivities(&controlStoreStub{status: &recordingStatusRecorder{err: recorderErr}}, nil)

	err := activities.RecordTemplateRunStatus(context.Background(), domain.TemplateRunStatusActivityInput{
		RunID:    domain.TemplateRunID("run_123"),
		TenantID: domain.TenantID("tenant_123"),
		Status:   domain.TemplateRunFailed,
	})
	if !errors.Is(err, recorderErr) {
		t.Fatalf("error = %v, want recorderErr", err)
	}
	if !strings.Contains(err.Error(), "record template run status") {
		t.Fatalf("error = %q, want status context", err.Error())
	}
}

func TestPrepareWorkspaceCreatesRunDirectory(t *testing.T) {
	t.Parallel()

	runRoot := t.TempDir()
	activities := NewTemplateRunActivities(runRoot, nil, nil, nil)
	input := domain.PrepareWorkspaceActivityInput{
		RunID:    domain.TemplateRunID("run_123"),
		TenantID: domain.TenantID("tenant_123"),
	}

	output, err := activities.PrepareWorkspace(context.Background(), input)
	if err != nil {
		t.Fatalf("PrepareWorkspace returned error: %v", err)
	}

	wantPath := filepath.Join(runRoot, "tenant_123", "run_123")
	if output.WorkspacePath != wantPath {
		t.Fatalf("WorkspacePath = %q, want %q", output.WorkspacePath, wantPath)
	}
	info, err := os.Stat(wantPath)
	if err != nil {
		t.Fatalf("stat workspace path: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("workspace path %q is not a directory", wantPath)
	}
}

func TestPrepareWorkspaceRejectsEmptyRoot(t *testing.T) {
	t.Parallel()

	activities := NewTemplateRunActivities("", nil, nil, nil)

	_, err := activities.PrepareWorkspace(context.Background(), domain.PrepareWorkspaceActivityInput{
		RunID:    domain.TemplateRunID("run_123"),
		TenantID: domain.TenantID("tenant_123"),
	})
	if err == nil {
		t.Fatal("PrepareWorkspace returned nil error, want error")
	}
	if !strings.Contains(err.Error(), "run root") {
		t.Fatalf("error = %q, want run root context", err.Error())
	}
}

func TestPrepareWorkspaceRejectsUnsafePathComponents(t *testing.T) {
	t.Parallel()

	activities := NewTemplateRunActivities(t.TempDir(), nil, nil, nil)

	_, err := activities.PrepareWorkspace(context.Background(), domain.PrepareWorkspaceActivityInput{
		RunID:    domain.TemplateRunID("run_123"),
		TenantID: domain.TenantID("../tenant_123"),
	})
	if err == nil {
		t.Fatal("PrepareWorkspace returned nil error, want error")
	}
	if !strings.Contains(err.Error(), "safe path") {
		t.Fatalf("error = %q, want safe path context", err.Error())
	}
}

// The revision is a commit, so the run checks out that commit. Following the
// ref instead would let the source move between a plan and the apply approved
// against it, and would ignore the revision entirely after an upgrade, since
// the installed ref is never updated to match the new revision.
func TestFetchSourceChecksOutTheResolvedCommitRatherThanTheRef(t *testing.T) {
	t.Parallel()

	workspacePath := t.TempDir()
	git := &recordingSourceGitRunner{}
	activities := &TemplateRunActivities{
		runRoot:         t.TempDir(),
		terraformRunner: &recordingTerraformRunner{},
		git:             git,
	}

	output, err := activities.FetchSource(context.Background(), domain.FetchSourceActivityInput{
		RunID:         domain.TemplateRunID("run_123"),
		TenantID:      domain.TenantID("tenant_123"),
		WorkspacePath: workspacePath,
		RepoOwner:     "acme",
		RepoName:      "infra-templates",
		// The ref the component was installed from is stale relative to the
		// revision this run is for; it must not decide what gets checked out.
		SourceRef:         "main",
		ResolvedCommitSHA: "a1b2c3d",
		RootPath:          "modules/vpc",
	})
	if err != nil {
		t.Fatalf("FetchSource returned error: %v", err)
	}

	if git.commitSHA != "a1b2c3d" {
		t.Fatalf("commitSHA = %q, want a1b2c3d", git.commitSHA)
	}
	if git.ref != "" {
		t.Fatalf("ref = %q, want the ref to be unused", git.ref)
	}
	if git.repoURL != "https://github.com/acme/infra-templates.git" {
		t.Fatalf("repoURL = %q", git.repoURL)
	}
	wantCloneDest := filepath.Join(workspacePath, "source")
	if git.dest != wantCloneDest {
		t.Fatalf("dest = %q, want %q", git.dest, wantCloneDest)
	}
	wantTerraformPath := filepath.Join(workspacePath, "source", "modules", "vpc")
	if output.TerraformPath != wantTerraformPath {
		t.Fatalf("TerraformPath = %q, want %q", output.TerraformPath, wantTerraformPath)
	}
}

// Runs queued before the commit was threaded through carry no commit, so they
// keep the old ref-based behaviour rather than failing. This path dies with the
// last such run.
func TestFetchSourceFallsBackToTheRefWhenNoCommitWasResolved(t *testing.T) {
	t.Parallel()

	workspacePath := t.TempDir()
	git := &recordingSourceGitRunner{}
	activities := &TemplateRunActivities{
		runRoot:         t.TempDir(),
		terraformRunner: &recordingTerraformRunner{},
		git:             git,
	}

	output, err := activities.FetchSource(context.Background(), domain.FetchSourceActivityInput{
		RunID:         domain.TemplateRunID("run_123"),
		TenantID:      domain.TenantID("tenant_123"),
		WorkspacePath: workspacePath,
		RepoOwner:     "acme",
		RepoName:      "infra-templates",
		SourceRef:     "main",
		RootPath:      "modules/vpc",
	})
	if err != nil {
		t.Fatalf("FetchSource returned error: %v", err)
	}

	if git.ref != "main" {
		t.Fatalf("ref = %q, want main", git.ref)
	}
	if git.commitSHA != "" {
		t.Fatalf("commitSHA = %q, want no commit checkout", git.commitSHA)
	}
	wantTerraformPath := filepath.Join(workspacePath, "source", "modules", "vpc")
	if output.TerraformPath != wantTerraformPath {
		t.Fatalf("TerraformPath = %q, want %q", output.TerraformPath, wantTerraformPath)
	}
}

func TestFetchSourceRejectsUnsafeRootPath(t *testing.T) {
	t.Parallel()

	git := &recordingSourceGitRunner{}
	activities := &TemplateRunActivities{
		runRoot:         t.TempDir(),
		terraformRunner: &recordingTerraformRunner{},
		git:             git,
	}

	_, err := activities.FetchSource(context.Background(), domain.FetchSourceActivityInput{
		RunID:         domain.TemplateRunID("run_123"),
		TenantID:      domain.TenantID("tenant_123"),
		WorkspacePath: t.TempDir(),
		RepoOwner:     "acme",
		RepoName:      "infra-templates",
		SourceRef:     "main",
		RootPath:      "../secret",
	})
	if err == nil {
		t.Fatal("FetchSource returned nil error, want unsafe root path error")
	}
	if git.repoURL != "" {
		t.Fatalf("git clone was called with repoURL %q", git.repoURL)
	}
}

func TestRunTerraformDelegatesToRunner(t *testing.T) {
	t.Parallel()

	runner := &recordingTerraformRunner{}
	activities := NewTemplateRunActivities(t.TempDir(), nil, nil, nil, runner)
	input := domain.RunTerraformActivityInput{
		RunID:         domain.TemplateRunID("run_123"),
		TenantID:      domain.TenantID("tenant_123"),
		WorkspacePath: "/tmp/tflive/runs/tenant_123/run_123",
		WorkspaceName: "mtp_acme_prod_vpc_a13f9c",
		Command:       domain.TerraformCommandPlan,
	}

	output, err := activities.RunTerraform(context.Background(), input)
	if err != nil {
		t.Fatalf("RunTerraform returned error: %v", err)
	}

	if !reflect.DeepEqual(runner.input, input) {
		t.Fatalf("runner input = %#v, want %#v", runner.input, input)
	}
	if output.Log != runner.log {
		t.Fatalf("output log = %#v, want %#v", output.Log, runner.log)
	}
}

func TestRecordTemplateRunLogDelegatesToRecorder(t *testing.T) {
	t.Parallel()

	recorder := &recordingLogMetadataRecorder{}
	log := domain.TemplateRunLog{TenantID: "tenant_123", RunID: "run_123", Phase: "plan", ObjectKey: "tenants/tenant_123/runs/run_123/logs/plan.log"}

	if err := NewControlActivities(&controlStoreStub{logs: recorder}, nil).RecordTemplateRunLog(context.Background(), log); err != nil {
		t.Fatalf("RecordTemplateRunLog returned error: %v", err)
	}
	if recorder.log != log {
		t.Fatalf("recorded log = %#v, want %#v", recorder.log, log)
	}

	recorder.err = errors.New("database unavailable")
	err := NewControlActivities(&controlStoreStub{logs: recorder}, nil).RecordTemplateRunLog(context.Background(), log)
	if !errors.Is(err, recorder.err) || !strings.Contains(err.Error(), "record template run log metadata") {
		t.Fatalf("error = %v, want wrapped recorder error", err)
	}
}

// A failed command's log is what explains the failure, so it must reach the
// workflow even though the activity errored: as ApplicationError details.
func TestRunTerraformAttachesUploadedLogToCommandFailure(t *testing.T) {
	t.Parallel()

	runnerErr := errors.New("terraform failed")
	log := domain.TemplateRunLog{TenantID: "tenant_123", RunID: "run_123", Phase: "apply", ObjectKey: "tenants/tenant_123/runs/run_123/logs/apply.log"}
	activities := NewTemplateRunActivities(t.TempDir(), nil, nil, nil, &recordingTerraformRunner{log: log, err: runnerErr})

	_, err := activities.RunTerraform(context.Background(), domain.RunTerraformActivityInput{
		RunID:    domain.TemplateRunID("run_123"),
		TenantID: domain.TenantID("tenant_123"),
		Command:  domain.TerraformCommandApply,
	})
	var applicationErr *temporal.ApplicationError
	if !errors.As(err, &applicationErr) {
		t.Fatalf("error = %T %v, want *temporal.ApplicationError", err, err)
	}
	if applicationErr.Type() != domain.TerraformCommandFailedErrorType {
		t.Fatalf("error type = %q, want %q", applicationErr.Type(), domain.TerraformCommandFailedErrorType)
	}
	if !errors.Is(err, runnerErr) || !strings.Contains(err.Error(), "run terraform") {
		t.Fatalf("error = %v, want run terraform context wrapping runnerErr", err)
	}
	var got domain.TemplateRunLog
	if err := applicationErr.Details(&got); err != nil {
		t.Fatalf("Details returned error: %v", err)
	}
	if got != log {
		t.Fatalf("details log = %#v, want %#v", got, log)
	}
}

func TestLocalTerraformRunnerWritesCommandLogFile(t *testing.T) {
	t.Parallel()

	workspacePath := t.TempDir()
	executor := &recordingCommandExecutor{
		stdout: "plan stdout\n",
		stderr: "plan stderr\n",
	}
	terraformRunner := localTerraformRunner{
		runner: gitrunner.NewLocalProcessRunnerWithExecutor(executor),
	}

	runOutput, err := terraformRunner.RunTerraform(context.Background(), domain.RunTerraformActivityInput{
		RunID:         domain.TemplateRunID("run_123"),
		TenantID:      domain.TenantID("tenant_123"),
		WorkspacePath: workspacePath,
		WorkspaceName: "mtp_acme_prod_vpc_a13f9c",
		Command:       domain.TerraformCommandPlan,
		ConfigJSON:    []byte(`{"region":"us-east-1"}`),
	})
	log := runOutput.Log
	if err != nil {
		t.Fatalf("RunTerraform returned error: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(workspacePath, "logs", "plan.log"))
	if err != nil {
		t.Fatalf("read plan log: %v", err)
	}
	if string(got) != "plan stdout\nplan stderr\n" {
		t.Fatalf("plan log = %q", string(got))
	}
	if log != (domain.TemplateRunLog{}) {
		t.Fatalf("log = %#v, want none without a log store", log)
	}
	if !reflect.DeepEqual(executor.env, []string{"TF_VAR_region=us-east-1"}) {
		t.Fatalf("env = %#v, want TF_VAR_region", executor.env)
	}
}

func TestLocalTerraformRunnerUploadsCommandLogFile(t *testing.T) {
	t.Parallel()

	workspacePath := t.TempDir()
	executor := &recordingCommandExecutor{
		stdout: "plan stdout\n",
		stderr: "plan stderr\n",
	}
	logStore := &recordingTemplateRunLogStore{}
	terraformRunner := localTerraformRunner{
		runner:   gitrunner.NewLocalProcessRunnerWithExecutor(executor),
		logStore: logStore,
	}

	runOutput, err := terraformRunner.RunTerraform(context.Background(), domain.RunTerraformActivityInput{
		RunID:         domain.TemplateRunID("run_123"),
		TenantID:      domain.TenantID("tenant_123"),
		WorkspacePath: workspacePath,
		WorkspaceName: "mtp_acme_prod_vpc_a13f9c",
		Command:       domain.TerraformCommandPlan,
	})
	log := runOutput.Log
	if err != nil {
		t.Fatalf("RunTerraform returned error: %v", err)
	}

	if logStore.tenantID != domain.TenantID("tenant_123") {
		t.Fatalf("tenantID = %q, want tenant_123", logStore.tenantID)
	}
	if logStore.runID != domain.TemplateRunID("run_123") {
		t.Fatalf("runID = %q, want run_123", logStore.runID)
	}
	if logStore.phase != "plan" {
		t.Fatalf("phase = %q, want plan", logStore.phase)
	}
	if logStore.content != "plan stdout\nplan stderr\n" {
		t.Fatalf("uploaded content = %q", logStore.content)
	}
	if log != logStore.returned {
		t.Fatalf("log = %#v, want the uploaded metadata %#v", log, logStore.returned)
	}
}

func TestLocalTerraformRunnerUploadsCommandLogWhenCommandFails(t *testing.T) {
	t.Parallel()

	runnerErr := errors.New("terraform failed")
	workspacePath := t.TempDir()
	executor := &recordingCommandExecutor{
		stdout: "plan stdout before failure\n",
		err:    runnerErr,
	}
	logStore := &recordingTemplateRunLogStore{}
	terraformRunner := localTerraformRunner{
		runner:   gitrunner.NewLocalProcessRunnerWithExecutor(executor),
		logStore: logStore,
	}

	runOutput, err := terraformRunner.RunTerraform(context.Background(), domain.RunTerraformActivityInput{
		RunID:         domain.TemplateRunID("run_123"),
		TenantID:      domain.TenantID("tenant_123"),
		WorkspacePath: workspacePath,
		WorkspaceName: "mtp_acme_prod_vpc_a13f9c",
		Command:       domain.TerraformCommandPlan,
	})
	log := runOutput.Log
	if !errors.Is(err, runnerErr) {
		t.Fatalf("error = %v, want runnerErr", err)
	}
	if logStore.content != "plan stdout before failure\n" {
		t.Fatalf("uploaded content = %q", logStore.content)
	}
	if log != logStore.returned {
		t.Fatalf("log = %#v, want the uploaded metadata alongside the error", log)
	}
}

// TestLocalTerraformRunnerHeartbeatsWhileTheCommandRuns covers what a
// heartbeat is for: an apply that legitimately runs for half an hour has to
// keep telling Temporal it is alive, or Temporal fails the activity at the
// heartbeat timeout long before the command's real budget is spent. The silent
// case is the important one -- a command producing no output at all still has
// to report, because output is not what keeps it alive.
func TestLocalTerraformRunnerHeartbeatsWhileTheCommandRuns(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		stdout string
	}{
		{name: "with output", stdout: "plan stdout\n"},
		{name: "silent", stdout: ""},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			executor := &blockingCommandExecutor{
				stdout:  testCase.stdout,
				release: make(chan struct{}),
			}
			heartbeats := newHeartbeatRecorder()
			terraformRunner := localTerraformRunner{
				runner:            gitrunner.NewLocalProcessRunnerWithExecutor(executor),
				heartbeat:         heartbeats.record,
				heartbeatInterval: time.Millisecond,
			}

			done := make(chan error, 1)
			go func() {
				_, err := terraformRunner.RunTerraform(context.Background(), domain.RunTerraformActivityInput{
					RunID:         domain.TemplateRunID("run_123"),
					TenantID:      domain.TenantID("tenant_123"),
					WorkspacePath: t.TempDir(),
					WorkspaceName: "mtp_acme_prod_vpc_a13f9c",
					Command:       domain.TerraformCommandPlan,
				})
				done <- err
			}()

			select {
			case <-heartbeats.first:
			case <-time.After(5 * time.Second):
				t.Fatal("no heartbeat was recorded while the command was running")
			}
			close(executor.release)
			if err := <-done; err != nil {
				t.Fatalf("RunTerraform returned error: %v", err)
			}
		})
	}
}

// TestLocalTerraformRunnerStopsHeartbeatingWhenTheCommandEnds guards the
// lifetime of the heartbeat goroutine. A heartbeat recorded after the activity
// returned is reported against a finished activity, and a goroutine per
// Terraform command that never exits is a leak on a long-lived executor.
func TestLocalTerraformRunnerStopsHeartbeatingWhenTheCommandEnds(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	close(release)
	executor := &blockingCommandExecutor{stdout: "plan stdout\n", release: release}
	heartbeats := newHeartbeatRecorder()
	terraformRunner := localTerraformRunner{
		runner:            gitrunner.NewLocalProcessRunnerWithExecutor(executor),
		heartbeat:         heartbeats.record,
		heartbeatInterval: time.Millisecond,
	}

	if _, err := terraformRunner.RunTerraform(context.Background(), domain.RunTerraformActivityInput{
		RunID:         domain.TemplateRunID("run_123"),
		TenantID:      domain.TenantID("tenant_123"),
		WorkspacePath: t.TempDir(),
		WorkspaceName: "mtp_acme_prod_vpc_a13f9c",
		Command:       domain.TerraformCommandPlan,
	}); err != nil {
		t.Fatalf("RunTerraform returned error: %v", err)
	}

	settled := heartbeats.count()
	time.Sleep(50 * time.Millisecond)
	if got := heartbeats.count(); got != settled {
		t.Fatalf("heartbeats after the command ended = %d, want none beyond the %d recorded during it", got-settled, settled)
	}
}

// TestRecordActivityHeartbeatIsSafeOffAnActivity pins the guard that lets the
// same runner be driven directly by tests and by tools: the Temporal SDK
// panics when asked to heartbeat on a context that is not an activity's.
func TestRecordActivityHeartbeatIsSafeOffAnActivity(t *testing.T) {
	t.Parallel()

	recordActivityHeartbeat(context.Background())
}

func TestRunTerraformWrapsRunnerError(t *testing.T) {
	t.Parallel()

	runnerErr := errors.New("terraform failed")
	activities := NewTemplateRunActivities(t.TempDir(), nil, nil, nil, &recordingTerraformRunner{err: runnerErr})

	_, err := activities.RunTerraform(context.Background(), domain.RunTerraformActivityInput{
		RunID:         domain.TemplateRunID("run_123"),
		TenantID:      domain.TenantID("tenant_123"),
		WorkspacePath: "/tmp/tflive/runs/tenant_123/run_123",
		WorkspaceName: "mtp_acme_prod_vpc_a13f9c",
		Command:       domain.TerraformCommandApply,
	})
	if !errors.Is(err, runnerErr) {
		t.Fatalf("error = %v, want runnerErr", err)
	}
	if !strings.Contains(err.Error(), "run terraform") {
		t.Fatalf("error = %q, want run terraform context", err.Error())
	}
}

type recordingStatusRecorder struct {
	input domain.TemplateRunStatusActivityInput
	err   error
}

func (recorder *recordingStatusRecorder) RecordTemplateRunStatus(_ context.Context, input domain.TemplateRunStatusActivityInput) error {
	recorder.input = input
	return recorder.err
}

type recordingTerraformRunner struct {
	input domain.RunTerraformActivityInput
	log   domain.TemplateRunLog
	err   error
}

func (runner *recordingTerraformRunner) RunTerraform(_ context.Context, input domain.RunTerraformActivityInput) (domain.RunTerraformActivityOutput, error) {
	runner.input = input
	return domain.RunTerraformActivityOutput{Log: runner.log}, runner.err
}

type recordingLogMetadataRecorder struct {
	log domain.TemplateRunLog
	err error
}

func (recorder *recordingLogMetadataRecorder) RecordTemplateRunLog(_ context.Context, log domain.TemplateRunLog) error {
	recorder.log = log
	return recorder.err
}

type recordingSourceGitRunner struct {
	repoURL    string
	ref        string
	commitSHA  string
	dest       string
	credential gitrunner.GitCredential
	err        error
}

func (runner *recordingSourceGitRunner) Clone(_ context.Context, repoURL string, ref string, dest string, credential gitrunner.GitCredential) error {
	runner.credential = credential
	runner.repoURL = repoURL
	runner.ref = ref
	runner.dest = dest
	if runner.err != nil {
		return runner.err
	}
	return os.MkdirAll(filepath.Join(dest, "modules", "vpc"), 0o700)
}

func (runner *recordingSourceGitRunner) CheckoutCommit(_ context.Context, repoURL string, commitSHA string, dest string, credential gitrunner.GitCredential) error {
	runner.credential = credential
	runner.repoURL = repoURL
	runner.commitSHA = commitSHA
	runner.dest = dest
	if runner.err != nil {
		return runner.err
	}
	return os.MkdirAll(filepath.Join(dest, "modules", "vpc"), 0o700)
}

func (runner *recordingSourceGitRunner) ResolveHead(context.Context, string) (string, error) {
	return "", nil
}

type recordingTemplateRunLogStore struct {
	tenantID domain.TenantID
	runID    domain.TemplateRunID
	phase    string
	content  string
	returned domain.TemplateRunLog
	err      error
}

func (store *recordingTemplateRunLogStore) PutTemplateRunLog(_ context.Context, tenantID domain.TenantID, runID domain.TemplateRunID, phase string, body io.Reader) (domain.TemplateRunLog, error) {
	store.tenantID = tenantID
	store.runID = runID
	store.phase = phase
	content, err := io.ReadAll(body)
	if err != nil {
		return domain.TemplateRunLog{}, err
	}
	store.content = string(content)
	store.returned = domain.TemplateRunLog{TenantID: tenantID, RunID: runID, Phase: phase, ObjectKey: "logs/" + phase + ".log", SizeBytes: int64(len(content))}
	return store.returned, store.err
}

// blockingCommandExecutor writes its output and then waits, standing in for an
// OpenTofu process that is still working when the heartbeat ticks.
type blockingCommandExecutor struct {
	stdout  string
	release chan struct{}
}

func (executor *blockingCommandExecutor) Run(ctx context.Context, _ string, _ []string, stdout io.Writer, _ io.Writer, _ string, _ ...string) error {
	if _, err := io.WriteString(stdout, executor.stdout); err != nil {
		return err
	}
	select {
	case <-executor.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// heartbeatRecorder counts what a running command reported and closes first as
// soon as anything is reported. Heartbeats arrive on the runner's own
// goroutine, so the recorder is guarded and never blocks it: a recorder that
// blocked would deadlock the stop it is observing.
type heartbeatRecorder struct {
	mutex sync.Mutex
	beats int
	first chan struct{}
	once  sync.Once
}

func newHeartbeatRecorder() *heartbeatRecorder {
	return &heartbeatRecorder{first: make(chan struct{})}
}

func (recorder *heartbeatRecorder) record(_ context.Context) {
	recorder.mutex.Lock()
	recorder.beats++
	recorder.mutex.Unlock()
	recorder.once.Do(func() { close(recorder.first) })
}

func (recorder *heartbeatRecorder) count() int {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	return recorder.beats
}

type recordingCommandExecutor struct {
	stdout string
	stderr string
	env    []string
	err    error
}

func (executor *recordingCommandExecutor) Run(_ context.Context, _ string, env []string, stdout io.Writer, stderr io.Writer, _ string, _ ...string) error {
	executor.env = append([]string(nil), env...)
	if _, err := io.WriteString(stdout, executor.stdout); err != nil {
		return err
	}
	if _, err := io.WriteString(stderr, executor.stderr); err != nil {
		return err
	}
	return executor.err
}

// fetchWithSealedToken drives a run's source fetch across the plane split: the
// control plane resolves and seals the token to a key the executor generated,
// then the executor opens it and checks out.
func fetchWithSealedToken(t *testing.T, tokens GitHubTokenSource, git gitrunner.GitRunner, owner string, repo string) error {
	t.Helper()

	keys := runseal.NewKeyRing()
	publicKey, err := keys.Generate(domain.RunKeyID("tenant_123", "run_123"))
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	sealed, err := NewControlActivities(nil, tokens).SealSourceToken(context.Background(), domain.SealSourceTokenActivityInput{
		RepoOwner: owner,
		RepoName:  repo,
		PublicKey: publicKey,
	})
	if err != nil {
		t.Fatalf("SealSourceToken returned error: %v", err)
	}
	if strings.Contains(string(sealed.SealedToken), "ghs_") {
		t.Fatal("sealed token contains the plaintext token")
	}

	executor := &TemplateRunActivities{
		runRoot:         t.TempDir(),
		terraformRunner: &recordingTerraformRunner{},
		git:             git,
		keys:            keys,
	}
	_, err = executor.FetchSource(context.Background(), domain.FetchSourceActivityInput{
		TenantID:          "tenant_123",
		RunID:             "run_123",
		WorkspacePath:     t.TempDir(),
		RepoOwner:         owner,
		RepoName:          repo,
		ResolvedCommitSHA: "a1b2c3d",
		RootPath:          "modules/vpc",
		SealedToken:       sealed.SealedToken,
		FetchHint:         sealed.FetchHint,
	})
	return err
}

// A run's checkout needs the credential just as a registration's clone does;
// this is the path that feeds Terraform.
func TestFetchSourcePassesInstallationTokenToCheckout(t *testing.T) {
	t.Parallel()

	git := &recordingSourceGitRunner{}
	if err := fetchWithSealedToken(t, stubTokenSource{token: "ghs_minted"}, git, "acme", "private-infra"); err != nil {
		t.Fatalf("FetchSource returned error: %v", err)
	}
	if git.credential != gitrunner.NewGitCredential("ghs_minted") {
		t.Fatal("checkout did not receive the installation credential")
	}
}

// A missing installation is not itself a failure. The checkout still goes
// ahead unauthenticated, and only once that checkout fails for its own reason
// does the missing installation become the user's problem.
func TestFetchSourceReportsUninstalledApp(t *testing.T) {
	t.Parallel()

	checkoutErr := errors.New("authentication required")
	err := fetchWithSealedToken(t, stubTokenSource{err: githubapp.ErrAppNotInstalled}, &recordingSourceGitRunner{err: checkoutErr}, "acme", "private-infra")
	if !errors.Is(err, checkoutErr) {
		t.Fatalf("error = %v, want it to wrap the underlying checkout failure", err)
	}
	if !strings.Contains(err.Error(), "acme/private-infra") {
		t.Fatalf("error = %v, want it to name the repository", err)
	}
	if !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("error = %v, want it to name the missing installation", err)
	}
}

// The regression this guards: configuring a GitHub App must not break
// fetching source from a public repository it was never installed on. A 404
// from the installation lookup cannot be told apart from that case, so the
// fetch must fall back to an unauthenticated checkout and succeed exactly as it
// would with no token source configured at all.
func TestFetchSourceSucceedsWithZeroCredentialWhenAppNotInstalled(t *testing.T) {
	t.Parallel()

	git := &recordingSourceGitRunner{}
	if err := fetchWithSealedToken(t, stubTokenSource{err: githubapp.ErrAppNotInstalled}, git, "hashicorp", "terraform-aws-modules"); err != nil {
		t.Fatalf("FetchSource returned error: %v", err)
	}
	if git.credential != (gitrunner.GitCredential{}) {
		t.Fatal("checkout received a non-zero credential for a repo the App is not installed on")
	}
}

// The same for a GitHub API failure that is not a missing installation: it must
// not fail a run against a public repository, which never needed the token.
func TestFetchSourceSucceedsWithZeroCredentialWhenTokenLookupFails(t *testing.T) {
	t.Parallel()

	git := &recordingSourceGitRunner{}
	tokens := stubTokenSource{err: errors.New("resolve installation: unexpected status 503 Service Unavailable")}
	if err := fetchWithSealedToken(t, tokens, git, "hashicorp", "terraform-aws-modules"); err != nil {
		t.Fatalf("FetchSource returned error: %v", err)
	}
	if git.credential != (gitrunner.GitCredential{}) {
		t.Fatal("checkout received a non-zero credential after the token lookup failed")
	}
}

// And when the unauthenticated checkout fails, the run's error must name the
// token lookup that failed rather than an installation that is fine.
func TestFetchSourceReportsTokenLookupFailure(t *testing.T) {
	t.Parallel()

	checkoutErr := errors.New("authentication required")
	lookupErr := errors.New("resolve installation: unexpected status 503 Service Unavailable")
	err := fetchWithSealedToken(t, stubTokenSource{err: lookupErr}, &recordingSourceGitRunner{err: checkoutErr}, "acme", "private-infra")
	if !errors.Is(err, checkoutErr) {
		t.Fatalf("error = %v, want it to wrap the underlying checkout failure", err)
	}
	if !strings.Contains(err.Error(), "503") {
		t.Fatalf("error = %v, want it to name the token lookup failure", err)
	}
	if strings.Contains(err.Error(), "not installed") {
		t.Fatalf("error = %v, must not blame a missing installation for an API failure", err)
	}
}

// With no App configured the fetch is unauthenticated and needs no hint.
func TestFetchSourceWithoutTokenSourceIsUnauthenticated(t *testing.T) {
	t.Parallel()

	git := &recordingSourceGitRunner{}
	if err := fetchWithSealedToken(t, nil, git, "hashicorp", "terraform-aws-modules"); err != nil {
		t.Fatalf("FetchSource returned error: %v", err)
	}
	if git.credential != (gitrunner.GitCredential{}) {
		t.Fatal("checkout received a credential with no token source configured")
	}
}

// Credentials reach Terraform only through the run's key: sealed on the control
// plane, opened on the executor, never present in plaintext in between.
func TestRunCredentialsRoundTripThroughTheRunKey(t *testing.T) {
	t.Parallel()

	keys := runseal.NewKeyRing()
	publicKey, err := keys.Generate(domain.RunKeyID("tenant_123", "run_123"))
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	store := &controlStoreStub{credentials: []domain.CredentialSet{
		{StackID: "stack_123", Name: "AWS_SECRET_ACCESS_KEY", Ciphertext: "canary-7f3a"},
	}}
	sealed, err := NewControlActivities(store, nil).SealRunCredentials(context.Background(), domain.SealRunCredentialsActivityInput{
		TenantID:        "tenant_123",
		StackTemplateID: "stack_template_123",
		PublicKey:       publicKey,
	})
	if err != nil {
		t.Fatalf("SealRunCredentials returned error: %v", err)
	}
	if strings.Contains(string(sealed.SealedEnvironment), "canary-7f3a") {
		t.Fatal("sealed environment contains the plaintext credential")
	}

	runner := &recordingTerraformRunner{}
	executor := NewTemplateRunActivities(t.TempDir(), nil, nil, keys, runner)
	if _, err := executor.RunTerraform(context.Background(), domain.RunTerraformActivityInput{
		TenantID:          "tenant_123",
		RunID:             "run_123",
		Command:           domain.TerraformCommandPlan,
		SealedEnvironment: sealed.SealedEnvironment,
	}); err != nil {
		t.Fatalf("RunTerraform returned error: %v", err)
	}
	if runner.input.Environment["AWS_SECRET_ACCESS_KEY"] != "decrypted:canary-7f3a" {
		t.Fatalf("terraform environment = %v, want the opened credential", runner.input.Environment)
	}

	// After release, the same sealed value is unreadable.
	if err := executor.ReleaseRunKey(context.Background(), domain.ReleaseRunKeyActivityInput{TenantID: "tenant_123", RunID: "run_123"}); err != nil {
		t.Fatalf("ReleaseRunKey returned error: %v", err)
	}
	_, err = executor.RunTerraform(context.Background(), domain.RunTerraformActivityInput{
		TenantID:          "tenant_123",
		RunID:             "run_123",
		Command:           domain.TerraformCommandPlan,
		SealedEnvironment: sealed.SealedEnvironment,
	})
	if !errors.Is(err, runseal.ErrNoKey) {
		t.Fatalf("error after release = %v, want runseal.ErrNoKey", err)
	}
}

// PrepareWorkspace hands back the key the run's secrets get sealed to.
func TestPrepareWorkspaceReturnsRunPublicKey(t *testing.T) {
	t.Parallel()

	keys := runseal.NewKeyRing()
	output, err := NewTemplateRunActivities(t.TempDir(), nil, nil, keys).PrepareWorkspace(context.Background(), domain.PrepareWorkspaceActivityInput{
		TenantID: "tenant_123",
		RunID:    "run_123",
	})
	if err != nil {
		t.Fatalf("PrepareWorkspace returned error: %v", err)
	}
	sealed, err := runseal.Seal(output.PublicKey, "value")
	if err != nil {
		t.Fatalf("Seal returned error: %v", err)
	}
	var opened string
	if err := keys.Open(domain.RunKeyID("tenant_123", "run_123"), sealed, &opened); err != nil || opened != "value" {
		t.Fatalf("Open = %q, %v; want the key PrepareWorkspace returned to open", opened, err)
	}
}

// controlStoreStub satisfies ControlStore, delegating the writes to recorders a
// test cares about.
type controlStoreStub struct {
	credentials []domain.CredentialSet
	status      *recordingStatusRecorder
	logs        *recordingLogMetadataRecorder
	planKey     []byte
	finished    domain.FinishPlanActivityInput
	outcome     domain.PlanOutcome
	claimed     bool
}

func (store *controlStoreStub) RecordTemplateRunStatus(ctx context.Context, input domain.TemplateRunStatusActivityInput) error {
	if store.status == nil {
		return nil
	}
	return store.status.RecordTemplateRunStatus(ctx, input)
}

func (store *controlStoreStub) RecordTemplateRunLog(ctx context.Context, log domain.TemplateRunLog) error {
	if store.logs == nil {
		return nil
	}
	return store.logs.RecordTemplateRunLog(ctx, log)
}

func (store *controlStoreStub) ListCredentialsForStackTemplate(context.Context, domain.TenantID, domain.StackTemplateID) ([]domain.CredentialSet, error) {
	return store.credentials, nil
}

func (*controlStoreStub) Decrypt(value string) (string, error) {
	return "decrypted:" + value, nil
}

func (store *controlStoreStub) CreatePlanKey(context.Context, domain.TenantID, domain.TemplateRunID) ([]byte, error) {
	return store.planKey, nil
}

func (store *controlStoreStub) PlanKey(context.Context, domain.TenantID, domain.TemplateRunID) ([]byte, error) {
	if store.planKey == nil {
		return nil, errors.New("run has no plan key")
	}
	return store.planKey, nil
}

func (store *controlStoreStub) FinishTemplatePlan(_ context.Context, input domain.FinishPlanActivityInput) (domain.PlanOutcome, error) {
	store.finished = input
	return store.outcome, nil
}

func (store *controlStoreStub) BeginTemplateApply(context.Context, domain.TenantID, domain.TemplateRunID) (bool, error) {
	return store.claimed, nil
}
