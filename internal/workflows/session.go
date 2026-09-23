package workflows

import (
	"time"

	"github.com/vishu42/tflive/internal/domain"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// session is one executor session and the work done in it. Its context is the
// session's, so every activity it schedules runs on the executor holding the
// run's workspace. It records nothing: steps, events and logs are the job's,
// and status is the run's.
//
// Its one control-plane dependency is seal: secrets the executor needs are
// sealed on the control plane to this session's key.
type session struct {
	ctx   workflow.Context
	seal  sealer
	input domain.TemplateRunWorkflowInput
	// phase is the half of the run this session serves, which names its
	// commands' logs.
	phase         domain.RunPhase
	workspacePath string
	terraformPath string
	// publicKey is the executor's sealing key for this run; everything secret
	// the executor needs is sealed to it on the control plane.
	publicKey []byte
}

// openSession creates an executor session. CreateSession takes its base queue
// from the context's activity options, so naming the execution queue here is
// what places the session, and every activity later scheduled on it, on an
// executor host.
func openSession(controlCtx workflow.Context, input domain.TemplateRunWorkflowInput, phase domain.RunPhase, creationTimeout time.Duration) (*session, error) {
	executionCtx := workflow.WithTaskQueue(controlCtx, domain.ExecutionTaskQueue)
	ctx, err := workflow.CreateSession(executionCtx, &workflow.SessionOptions{
		CreationTimeout:  creationTimeout,
		ExecutionTimeout: 24 * time.Hour,
	})
	if err != nil {
		return nil, err
	}
	return &session{
		ctx:   ctx,
		seal:  sealer{ctx: controlCtx, input: input},
		input: input,
		phase: phase,
	}, nil
}

// close tears the session down: the run's key is released and its workspace
// deleted, and with deletePlan its saved plan too. Teardown is best effort. A
// session that already failed has no executor left to clean up, and what it
// leaves behind is a directory and an unreadable plan file.
func (s *session) close(deletePlan bool) {
	s.releaseKey()
	s.cleanupWorkspace(deletePlan)
	workflow.CompleteSession(s.ctx)
}

// prepare creates the per-run filesystem workspace on the executor and keeps
// its path and the run's sealing key. Workflows cannot create directories
// directly because Temporal workflows must stay deterministic, so the side
// effect lives in PrepareWorkspace.
func (s *session) prepare() error {
	var output domain.PrepareWorkspaceActivityOutput
	if err := workflow.ExecuteActivity(
		s.ctx,
		domain.PrepareWorkspaceActivityName,
		domain.PrepareWorkspaceActivityInput{RunID: s.input.RunID, TenantID: s.input.TenantID},
	).Get(s.ctx, &output); err != nil {
		return err
	}
	s.workspacePath = output.WorkspacePath
	s.publicKey = output.PublicKey
	return nil
}

// fetchSource checks the run's commit out into the workspace, with a
// repository token sealed to this session's key.
func (s *session) fetchSource() error {
	token, err := s.seal.sourceToken(s.publicKey)
	if err != nil {
		return err
	}

	// A clone of a large repository can outlast the default one-minute budget,
	// so FetchSource gets a longer one of its own rather than raising the
	// default for every other activity in the run.
	fetchSourceCtx := workflow.WithActivityOptions(s.ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 3 * time.Minute,
		RetryPolicy:         defaultRunRetryPolicy,
	})

	var output domain.FetchSourceActivityOutput
	if err := workflow.ExecuteActivity(
		fetchSourceCtx,
		domain.FetchSourceActivityName,
		domain.FetchSourceActivityInput{
			RunID:             s.input.RunID,
			TenantID:          s.input.TenantID,
			WorkspacePath:     s.workspacePath,
			RepoOwner:         s.input.RepoOwner,
			RepoName:          s.input.RepoName,
			SourceRef:         s.input.SelectedRef,
			ResolvedCommitSHA: s.input.ResolvedCommitSHA,
			RootPath:          s.input.RootPath,
			SealedToken:       token.SealedToken,
			FetchHint:         token.FetchHint,
		},
	).Get(fetchSourceCtx, &output); err != nil {
		return err
	}
	s.terraformPath = output.TerraformPath
	return nil
}

// restorePlan puts the saved plan back into the workspace, with the plan key
// it was saved with, sealed to this session's key.
func (s *session) restorePlan() error {
	sealedPlanKey, err := s.seal.planKey(s.publicKey, false)
	if err != nil {
		return err
	}
	return s.planArtifact(domain.DownloadPlanActivityName, sealedPlanKey)
}

// savePlan uploads the plan the session just made, under a plan key created
// for it and sealed to this session's key.
func (s *session) savePlan() error {
	sealedPlanKey, err := s.seal.planKey(s.publicKey, true)
	if err != nil {
		return err
	}
	return s.planArtifact(domain.UploadPlanActivityName, sealedPlanKey)
}

// planArtifact uploads or downloads the run's saved plan on the executor.
func (s *session) planArtifact(activityName string, sealedPlanKey []byte) error {
	// A saved plan can run to tens of megabytes, more than the default budget
	// is sized for.
	artifactCtx := workflow.WithActivityOptions(s.ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		RetryPolicy:         defaultRunRetryPolicy,
	})
	return workflow.ExecuteActivity(artifactCtx, activityName, domain.PlanArtifactActivityInput{
		TenantID:      s.input.TenantID,
		RunID:         s.input.RunID,
		TerraformPath: s.terraformPath,
		SealedPlanKey: sealedPlanKey,
	}).Get(artifactCtx, nil)
}

// terraform runs one Terraform command on the executor, with the run's
// credentials sealed to this session's key.
func (s *session) terraform(command domain.TerraformCommandType) (domain.RunTerraformActivityOutput, error) {
	// Sealed for every command rather than once per run: credentials are read
	// fresh each time, and an apply can start a day after its plan.
	sealedEnvironment, err := s.seal.credentials(s.publicKey)
	if err != nil {
		return domain.RunTerraformActivityOutput{}, err
	}

	// Every Terraform command gets the configured budget and the more generous
	// retry policy, including init and workspace selection: init downloads
	// providers and modules over the network, which is no more predictable
	// than the plan that follows it.
	//
	// The heartbeat timeout is what distinguishes a command that is working
	// from one whose executor is gone: without it, a dead executor is
	// indistinguishable from a slow apply until the whole Terraform timeout
	// expires.
	terraformCtx := workflow.WithActivityOptions(s.ctx, workflow.ActivityOptions{
		StartToCloseTimeout: s.terraformTimeout(),
		HeartbeatTimeout:    domain.TerraformHeartbeatTimeout,
		RetryPolicy:         terraformRetryPolicy,
	})

	var output domain.RunTerraformActivityOutput
	err = workflow.ExecuteActivity(terraformCtx, domain.RunTerraformActivityName, domain.RunTerraformActivityInput{
		RunID:             s.input.RunID,
		TenantID:          s.input.TenantID,
		StackTemplateID:   s.input.StackTemplateID,
		WorkspacePath:     s.workspacePath,
		TerraformPath:     s.terraformPath,
		WorkspaceName:     s.input.WorkspaceName,
		Command:           command,
		ConfigJSON:        s.input.ConfigJSON,
		RunPhase:          s.phase,
		SealedEnvironment: sealedEnvironment,
	}).Get(terraformCtx, &output)
	return output, err
}

// releaseKey asks the executor to drop the run's sealing key. It is best effort:
// if the session already failed the executor is gone and its keys with it, and
// the key ring evicts abandoned keys on its own.
func (s *session) releaseKey() {
	if s.publicKey == nil {
		return
	}
	_ = workflow.ExecuteActivity(
		s.ctx,
		domain.ReleaseRunKeyActivityName,
		domain.ReleaseRunKeyActivityInput{TenantID: s.input.TenantID, RunID: s.input.RunID},
	).Get(s.ctx, nil)
}

// cleanupWorkspace asks the executor to delete the run's workspace, and with
// deletePlan its saved plan. Best effort, like releaseKey.
func (s *session) cleanupWorkspace(deletePlan bool) {
	if s.workspacePath == "" {
		return
	}
	_ = workflow.ExecuteActivity(
		s.ctx,
		domain.CleanupWorkspaceActivityName,
		domain.CleanupWorkspaceActivityInput{
			TenantID:      s.input.TenantID,
			RunID:         s.input.RunID,
			WorkspacePath: s.workspacePath,
			DeletePlan:    deletePlan,
		},
	).Get(s.ctx, nil)
}

// terraformTimeout is how long one Terraform command may run before Temporal
// fails it. It comes from the deployment's configuration, stamped onto the
// input when the run was dispatched, so a run keeps the budget it started with
// even if the control plane is reconfigured while it is in flight. Zero means
// nothing configured it, which is the default.
func (s *session) terraformTimeout() time.Duration {
	if s.input.TerraformTimeout > 0 {
		return s.input.TerraformTimeout
	}
	return domain.DefaultTerraformTimeout
}

// terraformRetryPolicy is applied to long-running Terraform commands (plan,
// apply). MaximumAttempts is temporarily pinned to 1 (no automatic retries) —
// in Temporal, 0 means unlimited attempts, not zero retries, so 1 is the
// value that disables retries.
var terraformRetryPolicy = &temporal.RetryPolicy{
	InitialInterval:    time.Minute,
	BackoffCoefficient: 2.0,
	MaximumInterval:    10 * time.Minute,
	MaximumAttempts:    1,
	NonRetryableErrorTypes: []string{
		"InvalidConfig",
		"UnsupportedCommand",
	},
}

// sealer seals secrets to a session's key on the control plane, so they cross
// Temporal as ciphertext only that executor can open. It reads secrets and
// can create the run's plan key, and nothing else: it has no status, step or
// event write.
type sealer struct {
	ctx   workflow.Context
	input domain.TemplateRunWorkflowInput
}

func (seal sealer) sourceToken(publicKey []byte) (domain.SealSourceTokenActivityOutput, error) {
	var output domain.SealSourceTokenActivityOutput
	err := workflow.ExecuteActivity(seal.ctx, domain.SealSourceTokenActivityName, domain.SealSourceTokenActivityInput{
		RepoOwner: seal.input.RepoOwner,
		RepoName:  seal.input.RepoName,
		PublicKey: publicKey,
	}).Get(seal.ctx, &output)
	return output, err
}

// planKey seals the run's plan key. The plan phase creates it; the apply
// phase reads the one the plan was saved with.
func (seal sealer) planKey(publicKey []byte, create bool) ([]byte, error) {
	var output domain.SealPlanKeyActivityOutput
	if err := workflow.ExecuteActivity(seal.ctx, domain.SealPlanKeyActivityName, domain.SealPlanKeyActivityInput{
		TenantID:  seal.input.TenantID,
		RunID:     seal.input.RunID,
		PublicKey: publicKey,
		Create:    create,
	}).Get(seal.ctx, &output); err != nil {
		return nil, err
	}
	return output.SealedPlanKey, nil
}

func (seal sealer) credentials(publicKey []byte) ([]byte, error) {
	var output domain.SealRunCredentialsActivityOutput
	if err := workflow.ExecuteActivity(seal.ctx, domain.SealRunCredentialsActivityName, domain.SealRunCredentialsActivityInput{
		TenantID:        seal.input.TenantID,
		StackTemplateID: seal.input.StackTemplateID,
		PublicKey:       publicKey,
	}).Get(seal.ctx, &output); err != nil {
		return nil, err
	}
	return output.SealedEnvironment, nil
}
