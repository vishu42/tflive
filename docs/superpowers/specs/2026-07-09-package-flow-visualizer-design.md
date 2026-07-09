# Package Flow Visualizer Design

## Purpose

Create a repo-local tool that helps visualize how Go packages in this module depend on each other. The first version focuses on package-level flow: commands import internal packages, and internal packages import each other. It should make the architecture easy to inspect without adding a server, npm build, or external runtime dependency.

## Scope

This slice includes:

- A standalone Go CLI under `cmd/pkgflow`.
- Reusable analysis and rendering code under `internal/pkgflow`.
- Discovery of Go packages through `go list -json ./...`.
- Package nodes for module-local packages.
- Directed import edges between module-local packages.
- Optional inclusion of external package nodes.
- A self-contained interactive HTML output.
- A Graphviz DOT output for sharing or static rendering.
- Focused unit tests for graph building and DOT output.

This slice does not include:

- Function-level or variable-level data-flow tracing.
- Runtime tracing.
- A long-running web server.
- Integration with the existing product UI.
- Dependency downloads or JavaScript build tooling.
- Cross-repository analysis.

## User Experience

The primary command generates an interactive HTML file:

```bash
go run ./cmd/pkgflow -out pkgflow.html
```

The generated file is self-contained and can be opened directly in a browser. It embeds the package graph JSON, CSS, and JavaScript in one file.

The DOT output is available through:

```bash
go run ./cmd/pkgflow -format dot -out pkgflow.dot
```

External packages are hidden by default so the graph answers the main question: how this repo's packages relate to each other. They can be included with:

```bash
go run ./cmd/pkgflow -include-external -out pkgflow.html
```

If `-out` is omitted, the tool writes to stdout. If `-dir` is omitted, it analyzes the current working directory.

## Architecture

The implementation is split into a small command and a focused internal package.

```text
cmd/pkgflow
  main.go          CLI flags, command execution, file/stdout output

internal/pkgflow
  analyzer.go      go list JSON loading and graph construction
  graph.go         graph, node, edge, and option types
  dot.go           Graphviz DOT renderer
  html.go          self-contained HTML renderer
  *_test.go        unit tests
```

The command package only handles I/O and flags. The internal package owns all behavior that should be tested.

## Graph Model

The graph has package nodes and directed edges.

```text
Node
  ID       full import path
  Label    short package name or command name
  Path     import path
  Dir      package directory from go list
  Group    cmd, internal, root, external
  External true when outside the current module

Edge
  From     importer package import path
  To       imported package import path
```

Edges are deduplicated and sorted for stable output. Nodes are also sorted by import path.

Groups are derived from the import path after the module path:

- `cmd`: local package path starts with `cmd/`
- `internal`: local package path starts with `internal/`
- `root`: local package is the module root
- `external`: package is outside the module

## Analysis Behavior

The analyzer shells out to:

```bash
go list -json ./...
```

It decodes the stream of JSON package objects rather than parsing text output. This keeps package metadata structured and avoids brittle string splitting.

For each package:

1. Record the package as a local node.
2. Inspect its `Imports`.
3. For imports inside the module path, add a local node when needed and add an edge.
4. For imports outside the module path, add nodes and edges only when `IncludeExternal` is true.

The tool should return a clear error when `go list` fails, including stderr from the command.

## HTML Rendering

The HTML renderer emits a complete document with embedded graph data. It uses plain SVG and vanilla JavaScript.

The first layout can be deterministic and simple:

- Place packages in vertical lanes by group.
- Sort packages alphabetically within each group.
- Draw edges as SVG paths between lanes.
- Use color to distinguish `cmd`, `internal`, `root`, and `external`.

The interaction model:

- Search filters/highlights matching package labels and paths.
- Clicking a node highlights incoming and outgoing edges.
- A reset control clears selection and search.
- A details panel shows the selected package path and direct imports/importers.

This gives immediate value without depending on a force-directed graph library.

## DOT Rendering

DOT output should be stable and valid:

- Graph name: `pkgflow`.
- Directed graph.
- One node per package.
- One edge per package import relationship.
- Quote and escape labels and IDs correctly.
- Group nodes with simple `subgraph cluster_*` blocks for readability.

## Error Handling

The CLI validates:

- `-format` must be `html` or `dot`.
- `-dir` must be non-empty.
- Output file writes must return actionable errors.

The analyzer reports:

- `go list` execution failure with stderr.
- Invalid JSON from `go list`.
- Missing module path in package metadata when no package reports a module.

## Testing

Tests should use in-memory `go list` JSON fixtures for graph construction so they do not depend on the current checkout's package list.

Required coverage:

- Local-only analysis includes module-local packages and local import edges.
- External analysis includes external nodes only when requested.
- Graph output is sorted and deduplicated.
- DOT renderer escapes quotes, backslashes, and newlines.

The final verification command is:

```bash
go test ./...
```

After tests pass, generate the tool's HTML output for this repo:

```bash
go run ./cmd/pkgflow -out pkgflow.html
```

## Future Extensions

The current graph is import flow, not function-level data flow. Later versions can add:

- Call edges between packages using `go/packages` and type information.
- Interface implementation edges.
- Runtime trace overlays.
- Filters for only `cmd -> internal`, only internal fan-in, or cycles.
- Export to JSON for other tools.
