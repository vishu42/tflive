package pkgflow

import (
	"fmt"
	"sort"
	"strings"
)

// RenderDOT renders graph as Graphviz DOT.
func RenderDOT(graph Graph) string {
	var b strings.Builder
	b.WriteString("digraph \"pkgflow\" {\n")
	b.WriteString("  rankdir=LR;\n")
	b.WriteString("  node [shape=box, style=\"rounded,filled\", fontname=\"Helvetica\"];\n")
	b.WriteString("  edge [color=\"#64748b\"];\n")

	byGroup := map[string][]Node{}
	for _, node := range graph.Nodes {
		byGroup[node.Group] = append(byGroup[node.Group], node)
	}

	for _, group := range sortedGroups(byGroup) {
		nodes := byGroup[group]
		sort.Slice(nodes, func(i, j int) bool {
			return nodes[i].ID < nodes[j].ID
		})

		fmt.Fprintf(&b, "  subgraph %s {\n", dotQuote("cluster_"+group))
		fmt.Fprintf(&b, "    label=%s;\n", dotQuote(group))
		for _, node := range nodes {
			fmt.Fprintf(&b, "    %s [label=%s, fillcolor=%s];\n",
				dotQuote(node.ID),
				dotQuote(node.Label),
				dotQuote(dotFillColor(node.Group)),
			)
		}
		b.WriteString("  }\n")
	}

	edges := append([]Edge(nil), graph.Edges...)
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From == edges[j].From {
			return edges[i].To < edges[j].To
		}
		return edges[i].From < edges[j].From
	})
	for _, edge := range edges {
		fmt.Fprintf(&b, "  %s -> %s;\n", dotQuote(edge.From), dotQuote(edge.To))
	}

	b.WriteString("}\n")
	return b.String()
}

func sortedGroups(byGroup map[string][]Node) []string {
	preferred := []string{"root", "cmd", "internal", "external"}
	groups := make([]string, 0, len(byGroup))
	seen := map[string]struct{}{}
	for _, group := range preferred {
		if _, ok := byGroup[group]; ok {
			groups = append(groups, group)
			seen[group] = struct{}{}
		}
	}

	var rest []string
	for group := range byGroup {
		if _, ok := seen[group]; !ok {
			rest = append(rest, group)
		}
	}
	sort.Strings(rest)
	groups = append(groups, rest...)
	return groups
}

func dotFillColor(group string) string {
	switch group {
	case "cmd":
		return "#dbeafe"
	case "internal":
		return "#dcfce7"
	case "external":
		return "#f1f5f9"
	default:
		return "#fef3c7"
	}
}

func dotQuote(value string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range value {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
