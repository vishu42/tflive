package pkgflow

// Graph is a package-level import graph for one Go module.
type Graph struct {
	ModulePath string `json:"modulePath"`
	Nodes      []Node `json:"nodes"`
	Edges      []Edge `json:"edges"`
}

// Node describes one Go package in the rendered graph.
type Node struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Path     string `json:"path"`
	Dir      string `json:"dir,omitempty"`
	Group    string `json:"group"`
	External bool   `json:"external"`
}

// Edge describes one directed import relationship between packages.
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Options controls graph construction.
type Options struct {
	IncludeExternal bool
}
