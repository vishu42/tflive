package pkgflow

import (
	"strings"
	"testing"
)

func TestRenderHTMLEmbedsGraphAndControls(t *testing.T) {
	t.Parallel()

	html, err := RenderHTML(Graph{
		ModulePath: "github.com/example/project",
		Nodes: []Node{
			{ID: "github.com/example/project/internal/api", Label: "api", Path: "github.com/example/project/internal/api", Group: "internal"},
		},
	})
	if err != nil {
		t.Fatalf("RenderHTML returned error: %v", err)
	}

	for _, want := range []string{
		"<!doctype html>",
		`id="search"`,
		`id="reset"`,
		`id="graph"`,
		`id="details"`,
		"github.com/example/project/internal/api",
		"Package Flow",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("HTML missing %q:\n%s", want, html)
		}
	}
}

func TestRenderHTMLEscapesEmbeddedJSONForScript(t *testing.T) {
	t.Parallel()

	html, err := RenderHTML(Graph{
		ModulePath: "github.com/example/project",
		Nodes: []Node{
			{ID: "github.com/example/project/internal/<api>", Label: "</script><script>", Path: "github.com/example/project/internal/<api>", Group: "internal"},
		},
	})
	if err != nil {
		t.Fatalf("RenderHTML returned error: %v", err)
	}

	if strings.Contains(html, "</script><script>") {
		t.Fatalf("HTML contains raw script-breaking label:\n%s", html)
	}
	if !strings.Contains(html, `\u003c/script\u003e\u003cscript\u003e`) {
		t.Fatalf("HTML does not contain escaped script label:\n%s", html)
	}
}
