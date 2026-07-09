package pkgflow

import (
	"strings"
	"testing"
)

func TestRenderDOTGroupsNodesAndRendersEdges(t *testing.T) {
	t.Parallel()

	dot := RenderDOT(Graph{
		Nodes: []Node{
			{ID: "github.com/example/project/cmd/api", Label: "api", Group: "cmd"},
			{ID: "github.com/example/project/internal/app", Label: "app", Group: "internal"},
		},
		Edges: []Edge{
			{From: "github.com/example/project/cmd/api", To: "github.com/example/project/internal/app"},
		},
	})

	for _, want := range []string{
		`digraph "pkgflow"`,
		`subgraph "cluster_cmd"`,
		`subgraph "cluster_internal"`,
		`"github.com/example/project/cmd/api" [label="api"`,
		`"github.com/example/project/cmd/api" -> "github.com/example/project/internal/app"`,
	} {
		if !strings.Contains(dot, want) {
			t.Fatalf("DOT missing %q:\n%s", want, dot)
		}
	}
}

func TestRenderDOTEscapesLabelsAndIDs(t *testing.T) {
	t.Parallel()

	dot := RenderDOT(Graph{
		Nodes: []Node{
			{ID: `github.com/example/project/internal/weird"name`, Label: "weird\nname", Group: "internal"},
			{ID: `github.com/example/project/cmd/api\tool`, Label: `api"tool`, Group: "cmd"},
		},
		Edges: []Edge{
			{From: `github.com/example/project/cmd/api\tool`, To: `github.com/example/project/internal/weird"name`},
		},
	})

	for _, want := range []string{
		`"github.com/example/project/internal/weird\"name"`,
		`label="weird\nname"`,
		`"github.com/example/project/cmd/api\\tool"`,
		`label="api\"tool"`,
	} {
		if !strings.Contains(dot, want) {
			t.Fatalf("DOT missing escaped value %q:\n%s", want, dot)
		}
	}
}
