# Package Flow Visualizer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a repo-local Go CLI that visualizes package import flow as self-contained HTML or Graphviz DOT.

**Architecture:** Keep CLI concerns in `cmd/pkgflow` and testable graph analysis/rendering in `internal/pkgflow`. Analyze packages from structured `go list -json ./...` output, build a stable package graph, then render HTML or DOT without new dependencies.

**Tech Stack:** Go 1.24, standard library only, `go list -json`, vanilla HTML/CSS/JS/SVG.

## Global Constraints

- Do not add npm, frontend build tooling, or a long-running server.
- Hide external packages by default.
- Keep output deterministic by sorting nodes and edges.
- Use test-first changes for production behavior.
- Final verification command: `go test ./...`.

---

### Task 1: Graph Model And Analyzer

**Files:**
- Create: `internal/pkgflow/graph.go`
- Create: `internal/pkgflow/analyzer.go`
- Test: `internal/pkgflow/analyzer_test.go`

**Interfaces:**
- Produces: `type Graph struct { ModulePath string; Nodes []Node; Edges []Edge }`
- Produces: `type Node struct { ID, Label, Path, Dir, Group string; External bool }`
- Produces: `type Edge struct { From, To string }`
- Produces: `type Options struct { IncludeExternal bool }`
- Produces: `func BuildGraphFromGoListJSON(r io.Reader, opts Options) (Graph, error)`
- Produces: `func AnalyzeDir(ctx context.Context, dir string, opts Options) (Graph, error)`

- [ ] **Step 1: Write failing analyzer tests**

Create `internal/pkgflow/analyzer_test.go` with tests like:

```go
func TestBuildGraphFromGoListJSONKeepsLocalPackagesOnlyByDefault(t *testing.T) {
	t.Parallel()

	graph, err := BuildGraphFromGoListJSON(strings.NewReader(goListFixture), Options{})
	if err != nil {
		t.Fatalf("BuildGraphFromGoListJSON returned error: %v", err)
	}

	assertNodeIDs(t, graph, []string{
		"github.com/example/project/cmd/api",
		"github.com/example/project/internal/api",
		"github.com/example/project/internal/app",
	})
	assertEdges(t, graph, []Edge{
		{From: "github.com/example/project/cmd/api", To: "github.com/example/project/internal/api"},
		{From: "github.com/example/project/internal/api", To: "github.com/example/project/internal/app"},
	})
}
```

Also test `IncludeExternal: true`, duplicate import deduplication, sorted output, and invalid JSON.

- [ ] **Step 2: Run analyzer tests and verify RED**

Run:

```bash
rtk go test ./internal/pkgflow -run TestBuildGraphFromGoListJSON -count=1
```

Expected: FAIL because `internal/pkgflow` and `BuildGraphFromGoListJSON` do not exist.

- [ ] **Step 3: Implement minimal graph model and analyzer**

Create `graph.go` with the exported types. Create `analyzer.go` with:

```go
func BuildGraphFromGoListJSON(r io.Reader, opts Options) (Graph, error)
func AnalyzeDir(ctx context.Context, dir string, opts Options) (Graph, error)
```

`BuildGraphFromGoListJSON` decodes the JSON stream, derives the module path from package `Module.Path`, records local packages, adds local import edges, optionally records external packages, then sorts and deduplicates.

`AnalyzeDir` runs `go list -json ./...` in the requested directory and passes stdout to `BuildGraphFromGoListJSON`.

- [ ] **Step 4: Run analyzer tests and verify GREEN**

Run:

```bash
rtk go test ./internal/pkgflow -run TestBuildGraphFromGoListJSON -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit task 1**

```bash
git add internal/pkgflow/graph.go internal/pkgflow/analyzer.go internal/pkgflow/analyzer_test.go
git commit -m "feat: add package flow analyzer"
```

---

### Task 2: DOT Renderer

**Files:**
- Create: `internal/pkgflow/dot.go`
- Test: `internal/pkgflow/dot_test.go`

**Interfaces:**
- Consumes: `pkgflow.Graph`, `pkgflow.Node`, `pkgflow.Edge`
- Produces: `func RenderDOT(graph Graph) string`

- [ ] **Step 1: Write failing DOT renderer tests**

Create `internal/pkgflow/dot_test.go` with tests like:

```go
func TestRenderDOTEscapesLabelsAndEdges(t *testing.T) {
	t.Parallel()

	dot := RenderDOT(Graph{
		Nodes: []Node{
			{ID: `github.com/example/project/internal/weird"name`, Label: "weird\nname", Group: "internal"},
			{ID: `github.com/example/project/cmd/api`, Label: "api", Group: "cmd"},
		},
		Edges: []Edge{
			{From: `github.com/example/project/cmd/api`, To: `github.com/example/project/internal/weird"name`},
		},
	})

	if !strings.Contains(dot, `digraph "pkgflow"`) {
		t.Fatalf("DOT missing graph header:\n%s", dot)
	}
	if !strings.Contains(dot, `"weird\nname"`) {
		t.Fatalf("DOT did not escape newline in label:\n%s", dot)
	}
	if !strings.Contains(dot, `\"name`) {
		t.Fatalf("DOT did not escape quote in ID:\n%s", dot)
	}
}
```

- [ ] **Step 2: Run DOT tests and verify RED**

Run:

```bash
rtk go test ./internal/pkgflow -run TestRenderDOT -count=1
```

Expected: FAIL because `RenderDOT` does not exist.

- [ ] **Step 3: Implement minimal DOT renderer**

Create `dot.go` with:

```go
func RenderDOT(graph Graph) string {
	var b strings.Builder
	// write graph header, grouped subgraphs, nodes, edges, and closing brace
	return b.String()
}
```

Use a helper that escapes `\`, `"`, and newline for quoted DOT strings.

- [ ] **Step 4: Run DOT tests and verify GREEN**

Run:

```bash
rtk go test ./internal/pkgflow -run TestRenderDOT -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit task 2**

```bash
git add internal/pkgflow/dot.go internal/pkgflow/dot_test.go
git commit -m "feat: render package flow dot"
```

---

### Task 3: HTML Renderer

**Files:**
- Create: `internal/pkgflow/html.go`
- Test: `internal/pkgflow/html_test.go`

**Interfaces:**
- Consumes: `pkgflow.Graph`
- Produces: `func RenderHTML(graph Graph) (string, error)`

- [ ] **Step 1: Write failing HTML renderer tests**

Create `internal/pkgflow/html_test.go` with tests like:

```go
func TestRenderHTMLEmbedsGraphAndControls(t *testing.T) {
	t.Parallel()

	html, err := RenderHTML(Graph{
		ModulePath: "github.com/example/project",
		Nodes: []Node{{ID: "github.com/example/project/internal/api", Label: "api", Group: "internal"}},
	})
	if err != nil {
		t.Fatalf("RenderHTML returned error: %v", err)
	}

	for _, want := range []string{
		"<!doctype html>",
		`id="search"`,
		`id="graph"`,
		`github.com/example/project/internal/api`,
		"Package Flow",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("HTML missing %q:\n%s", want, html)
		}
	}
}
```

- [ ] **Step 2: Run HTML tests and verify RED**

Run:

```bash
rtk go test ./internal/pkgflow -run TestRenderHTML -count=1
```

Expected: FAIL because `RenderHTML` does not exist.

- [ ] **Step 3: Implement self-contained HTML renderer**

Create `html.go` with:

```go
func RenderHTML(graph Graph) (string, error)
```

Marshal `graph` as JSON, embed it in the document, and include vanilla JavaScript for deterministic lane layout, search highlighting, node selection, edge highlighting, and details text.

- [ ] **Step 4: Run HTML tests and verify GREEN**

Run:

```bash
rtk go test ./internal/pkgflow -run TestRenderHTML -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit task 3**

```bash
git add internal/pkgflow/html.go internal/pkgflow/html_test.go
git commit -m "feat: render package flow html"
```

---

### Task 4: CLI Wiring

**Files:**
- Create: `cmd/pkgflow/main.go`
- Test: `cmd/pkgflow/main_test.go`

**Interfaces:**
- Consumes: `pkgflow.AnalyzeDir`, `pkgflow.RenderDOT`, `pkgflow.RenderHTML`
- Produces: CLI flags `-dir`, `-out`, `-format`, `-include-external`
- Produces: `func run(ctx context.Context, args []string, stdout, stderr io.Writer, analyze analyzerFunc, writeFile writeFileFunc) error`

- [ ] **Step 1: Write failing CLI tests**

Create `cmd/pkgflow/main_test.go` with tests like:

```go
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
```

Also test DOT output, invalid format, `-out` file write, and `-include-external` passing through to analyzer options.

- [ ] **Step 2: Run CLI tests and verify RED**

Run:

```bash
rtk go test ./cmd/pkgflow -count=1
```

Expected: FAIL because `cmd/pkgflow` does not exist.

- [ ] **Step 3: Implement CLI**

Create `cmd/pkgflow/main.go` with:

```go
func main() {
	if err := run(context.Background(), os.Args, os.Stdout, os.Stderr, pkgflow.AnalyzeDir, os.WriteFile); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
```

Use `flag.NewFlagSet`, validate `-format`, render HTML or DOT, and write to `-out` or stdout.

- [ ] **Step 4: Run CLI tests and verify GREEN**

Run:

```bash
rtk go test ./cmd/pkgflow -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit task 4**

```bash
git add cmd/pkgflow/main.go cmd/pkgflow/main_test.go
git commit -m "feat: add package flow cli"
```

---

### Task 5: End-To-End Verification And Output

**Files:**
- Create: `pkgflow.html`
- Modify: any implementation files only if verification finds issues.

**Interfaces:**
- Consumes: `go run ./cmd/pkgflow -out pkgflow.html`
- Produces: repo-local generated visualization at `pkgflow.html`

- [ ] **Step 1: Run full Go test suite**

Run:

```bash
rtk go test ./...
```

Expected: PASS.

- [ ] **Step 2: Generate package-flow HTML**

Run:

```bash
rtk go run ./cmd/pkgflow -out pkgflow.html
```

Expected: command exits 0 and creates `pkgflow.html`.

- [ ] **Step 3: Smoke-check generated HTML**

Run:

```bash
rtk rg -n "github.com/vishu42/megagega/internal/app|id=\"graph\"|Package Flow" pkgflow.html
```

Expected: finds all three strings.

- [ ] **Step 4: Commit task 5**

```bash
git add pkgflow.html
git commit -m "chore: generate package flow visualization"
```
