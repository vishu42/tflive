package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/vishu42/megagega/internal/pkgflow"
)

func TestRunWritesHTMLToStdoutByDefault(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	err := run(context.Background(), []string{"pkgflow"}, &stdout, io.Discard, fakeAnalyze, nil)
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "<!doctype html>") {
		t.Fatalf("stdout did not contain HTML:\n%s", stdout.String())
	}
}

func TestRunWritesDOTToStdout(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	err := run(context.Background(), []string{"pkgflow", "-format", "dot"}, &stdout, io.Discard, fakeAnalyze, nil)
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), `digraph "pkgflow"`) {
		t.Fatalf("stdout did not contain DOT:\n%s", stdout.String())
	}
}

func TestRunWritesOutputFile(t *testing.T) {
	t.Parallel()

	var wrotePath string
	var wroteData []byte
	err := run(context.Background(), []string{"pkgflow", "-out", "pkgflow.dot", "-format", "dot"}, io.Discard, io.Discard, fakeAnalyze, func(path string, data []byte, perm uint32) error {
		wrotePath = path
		wroteData = append([]byte(nil), data...)
		if perm != 0o644 {
			t.Fatalf("perm = %#o, want 0644", perm)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if wrotePath != "pkgflow.dot" {
		t.Fatalf("wrotePath = %q, want pkgflow.dot", wrotePath)
	}
	if !strings.Contains(string(wroteData), `digraph "pkgflow"`) {
		t.Fatalf("wroteData did not contain DOT:\n%s", string(wroteData))
	}
}

func TestRunRejectsInvalidFormat(t *testing.T) {
	t.Parallel()

	err := run(context.Background(), []string{"pkgflow", "-format", "json"}, io.Discard, io.Discard, fakeAnalyze, nil)
	if err == nil {
		t.Fatal("run returned nil error")
	}
	if !strings.Contains(err.Error(), `format must be "html" or "dot"`) {
		t.Fatalf("error = %q", err)
	}
}

func TestRunPassesIncludeExternalToAnalyzer(t *testing.T) {
	t.Parallel()

	var gotOptions pkgflow.Options
	analyze := func(ctx context.Context, dir string, opts pkgflow.Options) (pkgflow.Graph, error) {
		gotOptions = opts
		return fakeAnalyze(ctx, dir, opts)
	}

	err := run(context.Background(), []string{"pkgflow", "-include-external"}, io.Discard, io.Discard, analyze, nil)
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if !gotOptions.IncludeExternal {
		t.Fatal("IncludeExternal was not passed to analyzer")
	}
}

func TestRunWrapsAnalyzerError(t *testing.T) {
	t.Parallel()

	analyzeErr := errors.New("analyze failed")
	err := run(context.Background(), []string{"pkgflow"}, io.Discard, io.Discard, func(context.Context, string, pkgflow.Options) (pkgflow.Graph, error) {
		return pkgflow.Graph{}, analyzeErr
	}, nil)
	if !errors.Is(err, analyzeErr) {
		t.Fatalf("error = %v, want analyzeErr", err)
	}
	if !strings.Contains(err.Error(), "analyze packages") {
		t.Fatalf("error = %q, want analyze packages", err)
	}
}

func fakeAnalyze(context.Context, string, pkgflow.Options) (pkgflow.Graph, error) {
	return pkgflow.Graph{
		ModulePath: "github.com/example/project",
		Nodes: []pkgflow.Node{
			{ID: "github.com/example/project/cmd/api", Label: "api", Path: "github.com/example/project/cmd/api", Group: "cmd"},
			{ID: "github.com/example/project/internal/app", Label: "app", Path: "github.com/example/project/internal/app", Group: "internal"},
		},
		Edges: []pkgflow.Edge{
			{From: "github.com/example/project/cmd/api", To: "github.com/example/project/internal/app"},
		},
	}, nil
}
