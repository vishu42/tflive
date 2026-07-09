package pkgflow

import (
	"strings"
	"testing"
)

const goListFixture = `{
	"ImportPath": "github.com/example/project/cmd/api",
	"Name": "main",
	"Dir": "/repo/cmd/api",
	"Module": {"Path": "github.com/example/project"},
	"Imports": [
		"github.com/example/project/internal/api",
		"github.com/example/project/internal/api",
		"github.com/example/project/internal/app",
		"net/http"
	]
}
{
	"ImportPath": "github.com/example/project/internal/api",
	"Name": "api",
	"Dir": "/repo/internal/api",
	"Module": {"Path": "github.com/example/project"},
	"Imports": [
		"github.com/example/project/internal/app",
		"encoding/json"
	]
}
{
	"ImportPath": "github.com/example/project/internal/app",
	"Name": "app",
	"Dir": "/repo/internal/app",
	"Module": {"Path": "github.com/example/project"},
	"Imports": ["context"]
}
`

func TestBuildGraphFromGoListJSONKeepsLocalPackagesOnlyByDefault(t *testing.T) {
	t.Parallel()

	graph, err := BuildGraphFromGoListJSON(strings.NewReader(goListFixture), Options{})
	if err != nil {
		t.Fatalf("BuildGraphFromGoListJSON returned error: %v", err)
	}

	if graph.ModulePath != "github.com/example/project" {
		t.Fatalf("ModulePath = %q, want github.com/example/project", graph.ModulePath)
	}
	assertNodeIDs(t, graph, []string{
		"github.com/example/project/cmd/api",
		"github.com/example/project/internal/api",
		"github.com/example/project/internal/app",
	})
	assertEdges(t, graph, []Edge{
		{From: "github.com/example/project/cmd/api", To: "github.com/example/project/internal/api"},
		{From: "github.com/example/project/cmd/api", To: "github.com/example/project/internal/app"},
		{From: "github.com/example/project/internal/api", To: "github.com/example/project/internal/app"},
	})
}

func TestBuildGraphFromGoListJSONIncludesExternalPackagesWhenRequested(t *testing.T) {
	t.Parallel()

	graph, err := BuildGraphFromGoListJSON(strings.NewReader(goListFixture), Options{IncludeExternal: true})
	if err != nil {
		t.Fatalf("BuildGraphFromGoListJSON returned error: %v", err)
	}

	assertNodeIDs(t, graph, []string{
		"context",
		"encoding/json",
		"github.com/example/project/cmd/api",
		"github.com/example/project/internal/api",
		"github.com/example/project/internal/app",
		"net/http",
	})

	byID := map[string]Node{}
	for _, node := range graph.Nodes {
		byID[node.ID] = node
	}
	if !byID["net/http"].External {
		t.Fatal("net/http was not marked external")
	}
	if byID["net/http"].Group != "external" {
		t.Fatalf("net/http group = %q, want external", byID["net/http"].Group)
	}
}

func TestBuildGraphFromGoListJSONAssignsPackageGroupsAndLabels(t *testing.T) {
	t.Parallel()

	graph, err := BuildGraphFromGoListJSON(strings.NewReader(goListFixture), Options{})
	if err != nil {
		t.Fatalf("BuildGraphFromGoListJSON returned error: %v", err)
	}

	byID := map[string]Node{}
	for _, node := range graph.Nodes {
		byID[node.ID] = node
	}

	cmdNode := byID["github.com/example/project/cmd/api"]
	if cmdNode.Group != "cmd" {
		t.Fatalf("cmd group = %q, want cmd", cmdNode.Group)
	}
	if cmdNode.Label != "api" {
		t.Fatalf("cmd label = %q, want api", cmdNode.Label)
	}

	internalNode := byID["github.com/example/project/internal/app"]
	if internalNode.Group != "internal" {
		t.Fatalf("internal group = %q, want internal", internalNode.Group)
	}
	if internalNode.Label != "app" {
		t.Fatalf("internal label = %q, want app", internalNode.Label)
	}
}

func TestBuildGraphFromGoListJSONReturnsInvalidJSONError(t *testing.T) {
	t.Parallel()

	_, err := BuildGraphFromGoListJSON(strings.NewReader(`{"ImportPath":`), Options{})
	if err == nil {
		t.Fatal("BuildGraphFromGoListJSON returned nil error")
	}
	if !strings.Contains(err.Error(), "decode go list json") {
		t.Fatalf("error = %q, want decode go list json", err)
	}
}

func assertNodeIDs(t *testing.T, graph Graph, want []string) {
	t.Helper()

	got := make([]string, 0, len(graph.Nodes))
	for _, node := range graph.Nodes {
		got = append(got, node.ID)
	}
	assertStringSlices(t, got, want)
}

func assertEdges(t *testing.T, graph Graph, want []Edge) {
	t.Helper()

	if len(graph.Edges) != len(want) {
		t.Fatalf("edges = %#v, want %#v", graph.Edges, want)
	}
	for i := range want {
		if graph.Edges[i] != want[i] {
			t.Fatalf("edge[%d] = %#v, want %#v", i, graph.Edges[i], want[i])
		}
	}
}

func assertStringSlices(t *testing.T, got, want []string) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}
