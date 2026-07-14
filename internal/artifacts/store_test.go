package artifacts

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vishu42/tflive/internal/traits"
)

func TestLogKeyScopesByTenantRunAndPhase(t *testing.T) {
	t.Parallel()

	key, err := LogKey(traits.TenantID("tenant_123"), traits.TemplateRunID("run_123"), "plan")
	if err != nil {
		t.Fatalf("LogKey returned error: %v", err)
	}

	want := "tenants/tenant_123/runs/run_123/logs/plan.log"
	if key != want {
		t.Fatalf("key = %q, want %q", key, want)
	}
}

func TestLogKeyRejectsUnsafeComponents(t *testing.T) {
	t.Parallel()

	_, err := LogKey(traits.TenantID("tenant_123"), traits.TemplateRunID("run_123"), "../plan")
	if err == nil {
		t.Fatal("LogKey returned nil error for unsafe phase")
	}
	if !strings.Contains(err.Error(), "safe path") {
		t.Fatalf("error = %q, want safe path context", err.Error())
	}
}

func TestFilesystemStorePutsAndGetsObject(t *testing.T) {
	t.Parallel()

	store := NewFilesystemStore(t.TempDir())
	ctx := context.Background()

	err := store.PutObject(ctx, "tenants/tenant_123/runs/run_123/logs/plan.log", "text/plain; charset=utf-8", bytes.NewBufferString("plan output\n"))
	if err != nil {
		t.Fatalf("PutObject returned error: %v", err)
	}

	content, err := store.GetObject(ctx, "tenants/tenant_123/runs/run_123/logs/plan.log")
	if err != nil {
		t.Fatalf("GetObject returned error: %v", err)
	}
	if string(content) != "plan output\n" {
		t.Fatalf("content = %q, want plan output", string(content))
	}
}

func TestFilesystemStoreRejectsUnsafeKey(t *testing.T) {
	t.Parallel()

	store := NewFilesystemStore(t.TempDir())

	err := store.PutObject(context.Background(), "../plan.log", "text/plain", strings.NewReader("nope"))
	if err == nil {
		t.Fatal("PutObject returned nil error for unsafe key")
	}
	if !strings.Contains(err.Error(), "safe object key") {
		t.Fatalf("error = %q, want safe object key context", err.Error())
	}
}

func TestFilesystemStoreReturnsNotExistForMissingObject(t *testing.T) {
	t.Parallel()

	_, err := NewFilesystemStore(t.TempDir()).GetObject(context.Background(), "tenants/tenant_123/runs/run_123/logs/plan.log")
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("error = %v, want os.ErrNotExist", err)
	}
}

func TestLogStoreReadsAndWritesPhaseLogs(t *testing.T) {
	t.Parallel()

	store := NewLogStore(NewFilesystemStore(t.TempDir()))
	ctx := context.Background()

	err := store.PutTemplateRunLog(ctx, traits.TenantID("tenant_123"), traits.TemplateRunID("run_123"), "plan", strings.NewReader("plan output\n"))
	if err != nil {
		t.Fatalf("PutTemplateRunLog returned error: %v", err)
	}

	content, err := store.ReadTemplateRunLog(ctx, traits.TemplateRunLog{
		ObjectKey: "tenants/tenant_123/runs/run_123/logs/plan.log",
	})
	if err != nil {
		t.Fatalf("ReadTemplateRunLog returned error: %v", err)
	}
	if string(content) != "plan output\n" {
		t.Fatalf("content = %q, want plan output", string(content))
	}
}

func TestLogStoreRecordsMetadataAfterWritingPhaseLog(t *testing.T) {
	t.Parallel()

	recorder := &recordingLogMetadataRecorder{}
	store := NewRecordedLogStore(NewFilesystemStore(t.TempDir()), recorder)
	store.now = func() time.Time {
		return time.Date(2026, 7, 6, 10, 15, 0, 0, time.UTC)
	}

	err := store.PutTemplateRunLog(context.Background(), traits.TenantID("tenant_123"), traits.TemplateRunID("run_123"), "plan", strings.NewReader("plan output\n"))
	if err != nil {
		t.Fatalf("PutTemplateRunLog returned error: %v", err)
	}

	want := traits.TemplateRunLog{
		TenantID:    traits.TenantID("tenant_123"),
		RunID:       traits.TemplateRunID("run_123"),
		Phase:       "plan",
		ObjectKey:   "tenants/tenant_123/runs/run_123/logs/plan.log",
		ContentType: "text/plain; charset=utf-8",
		SizeBytes:   12,
		UploadedAt:  time.Date(2026, 7, 6, 10, 15, 0, 0, time.UTC),
	}
	if recorder.log != want {
		t.Fatalf("recorded metadata = %#v, want %#v", recorder.log, want)
	}
}

func TestS3StorePutsAndGetsObject(t *testing.T) {
	t.Parallel()

	transport := &recordingRoundTripper{}
	store, err := NewS3Store(S3Config{
		Bucket:          "tflive-artifacts",
		Region:          "us-east-1",
		Endpoint:        "https://s3.test.local",
		AccessKeyID:     "access-key",
		SecretAccessKey: "secret-key",
		ForcePathStyle:  true,
	})
	if err != nil {
		t.Fatalf("NewS3Store returned error: %v", err)
	}
	store.httpClient = &http.Client{Transport: transport}

	key := "tenants/tenant_123/runs/run_123/logs/plan.log"
	if err := store.PutObject(context.Background(), key, "text/plain; charset=utf-8", strings.NewReader("plan output\n")); err != nil {
		t.Fatalf("PutObject returned error: %v", err)
	}

	putRequest := transport.requests[0]
	if putRequest.URL.Path != "/tflive-artifacts/"+key {
		t.Fatalf("put path = %q, want /tflive-artifacts/%s", putRequest.URL.Path, key)
	}
	if !strings.Contains(putRequest.Header.Get("Authorization"), "AWS4-HMAC-SHA256") {
		t.Fatalf("Authorization = %q, want SigV4 scheme", putRequest.Header.Get("Authorization"))
	}

	content, err := store.GetObject(context.Background(), key)
	if err != nil {
		t.Fatalf("GetObject returned error: %v", err)
	}
	if string(content) != "plan output\n" {
		t.Fatalf("content = %q, want plan output", string(content))
	}
}

type recordingLogMetadataRecorder struct {
	log traits.TemplateRunLog
	err error
}

func (recorder *recordingLogMetadataRecorder) RecordTemplateRunLog(_ context.Context, log traits.TemplateRunLog) error {
	recorder.log = log
	return recorder.err
}

type recordingRoundTripper struct {
	requests []*http.Request
}

func (transport *recordingRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.requests = append(transport.requests, request)
	switch request.Method {
	case http.MethodPut:
		body, err := io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
		if string(body) != "plan output\n" {
			return nil, fmt.Errorf("put body = %q, want plan output", string(body))
		}
		if request.Header.Get("Content-Type") != "text/plain; charset=utf-8" {
			return nil, fmt.Errorf("Content-Type = %q", request.Header.Get("Content-Type"))
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("")),
			Header:     make(http.Header),
			Request:    request,
		}, nil
	case http.MethodGet:
		if request.URL.Path != "/tflive-artifacts/tenants/tenant_123/runs/run_123/logs/plan.log" {
			return nil, fmt.Errorf("get path = %q", request.URL.Path)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("plan output\n")),
			Header:     make(http.Header),
			Request:    request,
		}, nil
	}
	return nil, fmt.Errorf("method = %s, want PUT or GET", request.Method)
}

func TestFilesystemStoreWritesUnderRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := NewFilesystemStore(root)

	err := store.PutObject(context.Background(), "tenants/tenant_123/runs/run_123/logs/plan.log", "text/plain", strings.NewReader("plan output\n"))
	if err != nil {
		t.Fatalf("PutObject returned error: %v", err)
	}

	path := filepath.Join(root, "tenants", "tenant_123", "runs", "run_123", "logs", "plan.log")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stat object path: %v", err)
	}
}
