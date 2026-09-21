package activities

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/vishu42/tflive/internal/domain"
	"github.com/vishu42/tflive/internal/logsink"
	"github.com/vishu42/tflive/internal/planbundle"
	"github.com/vishu42/tflive/internal/runseal"
)

// memoryPlanStore keeps sealed plans in memory, keyed by run.
type memoryPlanStore struct {
	plans   map[string][]byte
	deleted []string
}

func newMemoryPlanStore() *memoryPlanStore {
	return &memoryPlanStore{plans: map[string][]byte{}}
}

func (store *memoryPlanStore) PutPlan(_ context.Context, tenantID domain.TenantID, runID domain.TemplateRunID, sealed []byte) error {
	store.plans[domain.RunKeyID(tenantID, runID)] = append([]byte(nil), sealed...)
	return nil
}

func (store *memoryPlanStore) GetPlan(_ context.Context, tenantID domain.TenantID, runID domain.TemplateRunID) ([]byte, error) {
	sealed, ok := store.plans[domain.RunKeyID(tenantID, runID)]
	if !ok {
		return nil, os.ErrNotExist
	}
	return sealed, nil
}

func (store *memoryPlanStore) DeletePlan(_ context.Context, tenantID domain.TenantID, runID domain.TemplateRunID) error {
	key := domain.RunKeyID(tenantID, runID)
	delete(store.plans, key)
	store.deleted = append(store.deleted, key)
	return nil
}

// The saved plan's whole journey: planned on one executor, carried through the
// store, applied on another. The control plane seals the same plan key to each
// executor's own run key, and the store only ever holds ciphertext.
func TestSavedPlanTravelsFromThePlanExecutorToTheApplyExecutor(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	plans := newMemoryPlanStore()
	control := NewControlActivities(&controlStoreStub{planKey: mustPlanKey(t)}, nil)

	// Plan phase, on the first executor.
	planKeys := runseal.NewKeyRing()
	planExecutor := NewTemplateRunActivities(t.TempDir(), nil, plans, planKeys)
	planWorkspace, err := planExecutor.PrepareWorkspace(ctx, domain.PrepareWorkspaceActivityInput{TenantID: "tenant_123", RunID: "run_123"})
	if err != nil {
		t.Fatalf("PrepareWorkspace returned error: %v", err)
	}
	planRoot := filepath.Join(planWorkspace.WorkspacePath, "source")
	writeTestFile(t, planRoot, "tfplan", "the reviewed plan")
	writeTestFile(t, planRoot, ".terraform.lock.hcl", "providers it was made with")
	sealedForPlan, err := control.SealPlanKey(ctx, domain.SealPlanKeyActivityInput{TenantID: "tenant_123", RunID: "run_123", PublicKey: planWorkspace.PublicKey, Create: true})
	if err != nil {
		t.Fatalf("SealPlanKey returned error: %v", err)
	}
	if err := planExecutor.UploadPlan(ctx, domain.PlanArtifactActivityInput{TenantID: "tenant_123", RunID: "run_123", TerraformPath: planRoot, SealedPlanKey: sealedForPlan.SealedPlanKey}); err != nil {
		t.Fatalf("UploadPlan returned error: %v", err)
	}
	stored := plans.plans["tenant_123/run_123"]
	if len(stored) == 0 || bytes.Contains(stored, []byte("the reviewed plan")) {
		t.Fatalf("stored plan = %q, want ciphertext", stored)
	}

	// Apply phase, on a second executor with its own run key.
	applyKeys := runseal.NewKeyRing()
	applyExecutor := NewTemplateRunActivities(t.TempDir(), nil, plans, applyKeys)
	applyWorkspace, err := applyExecutor.PrepareWorkspace(ctx, domain.PrepareWorkspaceActivityInput{TenantID: "tenant_123", RunID: "run_123"})
	if err != nil {
		t.Fatalf("PrepareWorkspace returned error: %v", err)
	}
	applyRoot := filepath.Join(applyWorkspace.WorkspacePath, "source")
	writeTestFile(t, applyRoot, ".terraform.lock.hcl", "providers released since")
	sealedForApply, err := control.SealPlanKey(ctx, domain.SealPlanKeyActivityInput{TenantID: "tenant_123", RunID: "run_123", PublicKey: applyWorkspace.PublicKey})
	if err != nil {
		t.Fatalf("SealPlanKey returned error: %v", err)
	}
	// The plan executor's sealed key means nothing to the apply executor.
	if err := applyExecutor.DownloadPlan(ctx, domain.PlanArtifactActivityInput{TenantID: "tenant_123", RunID: "run_123", TerraformPath: applyRoot, SealedPlanKey: sealedForPlan.SealedPlanKey}); err == nil {
		t.Fatal("DownloadPlan opened a key sealed to another executor")
	}
	if err := applyExecutor.DownloadPlan(ctx, domain.PlanArtifactActivityInput{TenantID: "tenant_123", RunID: "run_123", TerraformPath: applyRoot, SealedPlanKey: sealedForApply.SealedPlanKey}); err != nil {
		t.Fatalf("DownloadPlan returned error: %v", err)
	}
	assertTestFile(t, applyRoot, "tfplan", "the reviewed plan")
	assertTestFile(t, applyRoot, ".terraform.lock.hcl", "providers it was made with")
}

// A bundle stored under one run cannot be applied as another's, even by an
// executor holding the right key for that other run: it is bound to its run.
func TestDownloadPlanRefusesAPlanSavedForAnotherRun(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	key := mustPlanKey(t)
	plans := newMemoryPlanStore()
	bundle := mustBundle(t)
	sealed, err := planbundle.Seal(key, bundle, "tenant_123/run_other")
	if err != nil {
		t.Fatal(err)
	}
	plans.plans["tenant_123/run_123"] = sealed

	keys := runseal.NewKeyRing()
	executor := NewTemplateRunActivities(t.TempDir(), nil, plans, keys)
	workspace, err := executor.PrepareWorkspace(ctx, domain.PrepareWorkspaceActivityInput{TenantID: "tenant_123", RunID: "run_123"})
	if err != nil {
		t.Fatal(err)
	}
	sealedKey, err := runseal.Seal(workspace.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := executor.DownloadPlan(ctx, domain.PlanArtifactActivityInput{TenantID: "tenant_123", RunID: "run_123", TerraformPath: root, SealedPlanKey: sealedKey}); err == nil {
		t.Fatal("DownloadPlan accepted a plan sealed for another run")
	}
	if _, err := os.Stat(filepath.Join(root, "tfplan")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("plan was unpacked anyway: %v", err)
	}
}

// Cleanup deletes the run's own workspace, found from the run's identity and
// not from the path it was given, and on the apply phase the saved plan too.
func TestCleanupWorkspaceRemovesTheRunWorkspaceAndOptionallyThePlan(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	runRoot := t.TempDir()
	plans := newMemoryPlanStore()
	executor := NewTemplateRunActivities(runRoot, nil, plans, runseal.NewKeyRing())
	workspace, err := executor.PrepareWorkspace(ctx, domain.PrepareWorkspaceActivityInput{TenantID: "tenant_123", RunID: "run_123"})
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, workspace.WorkspacePath, "leftover", "x")
	outside := t.TempDir()
	writeTestFile(t, outside, "keep", "x")

	if err := executor.CleanupWorkspace(ctx, domain.CleanupWorkspaceActivityInput{TenantID: "tenant_123", RunID: "run_123", WorkspacePath: outside}); err != nil {
		t.Fatalf("CleanupWorkspace returned error: %v", err)
	}
	if _, err := os.Stat(workspace.WorkspacePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("run workspace still exists: %v", err)
	}
	assertTestFile(t, outside, "keep", "x")
	if len(plans.deleted) != 0 {
		t.Fatalf("plan deleted on the plan phase: %v", plans.deleted)
	}

	if err := executor.CleanupWorkspace(ctx, domain.CleanupWorkspaceActivityInput{TenantID: "tenant_123", RunID: "run_123", DeletePlan: true}); err != nil {
		t.Fatalf("CleanupWorkspace returned error: %v", err)
	}
	if len(plans.deleted) != 1 || plans.deleted[0] != "tenant_123/run_123" {
		t.Fatalf("deleted plans = %v, want the run's", plans.deleted)
	}
	wantPath, _ := logsink.RunWorkspacePath(runRoot, "tenant_123", "run_123")
	if wantPath != workspace.WorkspacePath {
		t.Fatalf("workspace path = %q, want %q", workspace.WorkspacePath, wantPath)
	}
}

// The apply phase reads the key the plan was saved with and never makes one:
// a run without a key is a terminal run, and nothing should open its plan.
func TestSealPlanKeyOnlyCreatesOnThePlanPhase(t *testing.T) {
	t.Parallel()

	control := NewControlActivities(&controlStoreStub{}, nil)
	keys := runseal.NewKeyRing()
	publicKey, err := keys.Generate("tenant_123/run_123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.SealPlanKey(context.Background(), domain.SealPlanKeyActivityInput{TenantID: "tenant_123", RunID: "run_123", PublicKey: publicKey}); err == nil {
		t.Fatal("SealPlanKey without Create returned a key for a run that has none")
	}
}

// FinishPlan hands the plan's result to the store and returns its decision.
func TestFinishPlanReturnsTheStoresOutcome(t *testing.T) {
	t.Parallel()

	store := &controlStoreStub{outcome: domain.PlanOutcomeApproved}
	control := NewControlActivities(store, nil)
	input := domain.FinishPlanActivityInput{TenantID: "tenant_123", RunID: "run_123", HasChanges: true, Summary: domain.PlanSummary{Add: 1}, AutoApprove: true}

	outcome, err := control.FinishPlan(context.Background(), input)
	if err != nil {
		t.Fatalf("FinishPlan returned error: %v", err)
	}
	if outcome != domain.PlanOutcomeApproved || store.finished != input {
		t.Fatalf("outcome = %q, finished = %#v", outcome, store.finished)
	}
}

func mustPlanKey(t *testing.T) []byte {
	t.Helper()
	key, err := planbundle.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func mustBundle(t *testing.T) []byte {
	t.Helper()
	dir := t.TempDir()
	writeTestFile(t, dir, "tfplan", "plan")
	bundle, err := planbundle.Pack(dir)
	if err != nil {
		t.Fatal(err)
	}
	return bundle
}

func writeTestFile(t *testing.T, dir string, name string, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertTestFile(t *testing.T, dir string, name string, want string) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	if string(got) != want {
		t.Fatalf("%s = %q, want %q", name, got, want)
	}
}
