package resorch

import (
	"fmt"
	"strings"
)

type GraphNode struct {
	ID     ID     `json:"id"`
	Driver string `json:"driver"`
}

// GraphEdge means "From depends on To".
type GraphEdge struct {
	From ID `json:"from"`
	To   ID `json:"to"`
}

type Graph struct {
	Nodes     []GraphNode `json:"nodes"`
	Edges     []GraphEdge `json:"edges"`
	TopoOrder []ID        `json:"topoOrder"`
}

func (g Graph) clone() Graph {
	cloned := Graph{
		Nodes:     make([]GraphNode, len(g.Nodes)),
		Edges:     make([]GraphEdge, len(g.Edges)),
		TopoOrder: make([]ID, len(g.TopoOrder)),
	}
	copy(cloned.Nodes, g.Nodes)
	copy(cloned.Edges, g.Edges)
	copy(cloned.TopoOrder, g.TopoOrder)
	return cloned
}

// DOT exports Graphviz DOT text.
func (g Graph) DOT() string {
	var b strings.Builder
	b.WriteString("digraph resorch {\n")
	b.WriteString("  rankdir=LR;\n")

	aliases := make(map[string]string, len(g.Nodes))
	for i, n := range g.Nodes {
		alias := fmt.Sprintf("n%d", i)
		aliases[n.ID.String()] = alias
		label := escapeDOT(n.ID.String())
		if n.Driver != "" {
			label = label + "\\n(" + escapeDOT(n.Driver) + ")"
		}
		b.WriteString(fmt.Sprintf("  %s [label=\"%s\"];\n", alias, label))
	}
	for _, e := range g.Edges {
		from, okFrom := aliases[e.From.String()]
		to, okTo := aliases[e.To.String()]
		if !okFrom || !okTo {
			continue
		}
		b.WriteString(fmt.Sprintf("  %s -> %s;\n", from, to))
	}
	b.WriteString("}\n")
	return b.String()
}

// Mermaid exports Mermaid graph text.
func (g Graph) Mermaid() string {
	var b strings.Builder
	b.WriteString("graph TD\n")

	aliases := make(map[string]string, len(g.Nodes))
	for i, n := range g.Nodes {
		alias := fmt.Sprintf("n%d", i)
		aliases[n.ID.String()] = alias
		label := escapeMermaid(n.ID.String())
		if n.Driver != "" {
			label = label + "<br/>(" + escapeMermaid(n.Driver) + ")"
		}
		b.WriteString(fmt.Sprintf("    %s[\"%s\"]\n", alias, label))
	}
	for _, e := range g.Edges {
		from, okFrom := aliases[e.From.String()]
		to, okTo := aliases[e.To.String()]
		if !okFrom || !okTo {
			continue
		}
		b.WriteString(fmt.Sprintf("    %s --> %s\n", from, to))
	}
	return b.String()
}

func escapeDOT(s string) string {
	return strings.ReplaceAll(s, "\"", "\\\"")
}

func escapeMermaid(s string) string {
	return strings.ReplaceAll(s, "\"", "\\\"")
}
