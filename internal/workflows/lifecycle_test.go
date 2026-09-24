package workflows

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// statusWriters are the activities that write a status, each with the one
// function allowed to schedule it. BeginApply and FinishPlan are here because
// they move a run's status as part of their own work.
var statusWriters = map[string]string{
	"RecordTemplateRunStatusActivityName":          "(*run).lifecycle",
	"BeginApplyActivityName":                       "(*run).lifecycle",
	"FinishPlanActivityName":                       "(*run).lifecycle",
	"RecordTemplateRegistrationStatusActivityName": "(*registration).lifecycle",
}

// TestOnlyLifecycleWritesStatus pins that a workflow's status is written in one
// place: every activity that writes it is named only in its lifecycle, so
// reading lifecycle is reading every status change. It also fails when a
// guarded name appears nowhere, so a renamed activity cannot leave it passing
// while guarding nothing.
func TestOnlyLifecycleWritesStatus(t *testing.T) {
	t.Parallel()

	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	seen := map[string]bool{}
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			owner := "package scope"
			if fn, ok := decl.(*ast.FuncDecl); ok {
				owner = funcName(fn)
			}
			ast.Inspect(decl, func(node ast.Node) bool {
				sel, ok := node.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				want, ok := statusWriters[sel.Sel.Name]
				if !ok {
					return true
				}
				seen[sel.Sel.Name] = true
				if owner != want {
					t.Errorf("%s: %s names %s; only %s may write status", fset.Position(sel.Pos()), owner, sel.Sel.Name, want)
				}
				return true
			})
		}
	}
	for name := range statusWriters {
		if !seen[name] {
			t.Errorf("nothing names %s: the guard is out of date", name)
		}
	}
}

// funcName names a function as the guard reports it: (*run).lifecycle for a
// method, lifecycle for a function.
func funcName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	switch recv := fn.Recv.List[0].Type.(type) {
	case *ast.StarExpr:
		if ident, ok := recv.X.(*ast.Ident); ok {
			return "(*" + ident.Name + ")." + fn.Name.Name
		}
	case *ast.Ident:
		return recv.Name + "." + fn.Name.Name
	}
	return fn.Name.Name
}
