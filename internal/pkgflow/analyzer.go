package pkgflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path"
	"sort"
	"strings"
)

type goListPackage struct {
	ImportPath string `json:"ImportPath"`
	Name       string `json:"Name"`
	Dir        string `json:"Dir"`
	Imports    []string
	Module     *struct {
		Path string `json:"Path"`
	} `json:"Module"`
}

// AnalyzeDir runs go list in dir and returns a package import graph.
func AnalyzeDir(ctx context.Context, dir string, opts Options) (Graph, error) {
	if strings.TrimSpace(dir) == "" {
		return Graph{}, errors.New("analyze dir: dir is required")
	}

	cmd := exec.CommandContext(ctx, "go", "list", "-json", "./...")
	cmd.Dir = dir

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return Graph{}, fmt.Errorf("go list failed: %s", msg)
	}

	graph, err := BuildGraphFromGoListJSON(&stdout, opts)
	if err != nil {
		return Graph{}, err
	}
	return graph, nil
}

// BuildGraphFromGoListJSON builds a package import graph from go list -json output.
func BuildGraphFromGoListJSON(r io.Reader, opts Options) (Graph, error) {
	decoder := json.NewDecoder(r)

	var packages []goListPackage
	modulePath := ""
	for {
		var pkg goListPackage
		if err := decoder.Decode(&pkg); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return Graph{}, fmt.Errorf("decode go list json: %w", err)
		}
		if pkg.ImportPath == "" {
			continue
		}
		if modulePath == "" && pkg.Module != nil {
			modulePath = pkg.Module.Path
		}
		packages = append(packages, pkg)
	}

	if modulePath == "" {
		return Graph{}, errors.New("go list json missing module path")
	}

	nodes := map[string]Node{}
	edges := map[Edge]struct{}{}
	for _, pkg := range packages {
		nodes[pkg.ImportPath] = nodeForPackage(modulePath, pkg.ImportPath, pkg.Name, pkg.Dir, false)
	}

	for _, pkg := range packages {
		for _, imported := range pkg.Imports {
			if isLocalImport(modulePath, imported) {
				if _, ok := nodes[imported]; !ok {
					nodes[imported] = nodeForPackage(modulePath, imported, "", "", false)
				}
				edges[Edge{From: pkg.ImportPath, To: imported}] = struct{}{}
				continue
			}
			if opts.IncludeExternal {
				if _, ok := nodes[imported]; !ok {
					nodes[imported] = nodeForPackage(modulePath, imported, "", "", true)
				}
				edges[Edge{From: pkg.ImportPath, To: imported}] = struct{}{}
			}
		}
	}

	graph := Graph{
		ModulePath: modulePath,
		Nodes:      sortedNodes(nodes),
		Edges:      sortedEdges(edges),
	}
	return graph, nil
}

func nodeForPackage(modulePath, importPath, packageName, dir string, external bool) Node {
	label := packageName
	if label == "" || label == "main" {
		label = path.Base(importPath)
	}

	return Node{
		ID:       importPath,
		Label:    label,
		Path:     importPath,
		Dir:      dir,
		Group:    groupForImport(modulePath, importPath, external),
		External: external,
	}
}

func groupForImport(modulePath, importPath string, external bool) string {
	if external {
		return "external"
	}

	rel := strings.TrimPrefix(importPath, modulePath)
	rel = strings.TrimPrefix(rel, "/")
	switch {
	case rel == "":
		return "root"
	case rel == "cmd" || strings.HasPrefix(rel, "cmd/"):
		return "cmd"
	case rel == "internal" || strings.HasPrefix(rel, "internal/"):
		return "internal"
	default:
		return "root"
	}
}

func isLocalImport(modulePath, importPath string) bool {
	return importPath == modulePath || strings.HasPrefix(importPath, modulePath+"/")
}

func sortedNodes(nodes map[string]Node) []Node {
	ids := make([]string, 0, len(nodes))
	for id := range nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	out := make([]Node, 0, len(ids))
	for _, id := range ids {
		out = append(out, nodes[id])
	}
	return out
}

func sortedEdges(edges map[Edge]struct{}) []Edge {
	out := make([]Edge, 0, len(edges))
	for edge := range edges {
		out = append(out, edge)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].From == out[j].From {
			return out[i].To < out[j].To
		}
		return out[i].From < out[j].From
	})
	return out
}
