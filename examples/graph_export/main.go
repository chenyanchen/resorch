package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/chenyanchen/resorch"
)

type nodeOpt struct {
	Dep string `json:"dep"`
}

func main() {
	reg := resorch.NewRegistry()
	resorch.MustRegister(reg, "node", "dep", resorch.Definition[nodeOpt, struct{}]{
		Deps: func(opt nodeOpt) ([]resorch.ID, error) {
			if opt.Dep == "" {
				return nil, nil
			}
			return []resorch.ID{{Kind: "node", Name: opt.Dep}}, nil
		},
		Build: func(_ context.Context, _ resorch.Resolver, _ nodeOpt) (struct{}, error) {
			return struct{}{}, nil
		},
	})

	container, err := resorch.NewContainer(reg, []resorch.NodeSpec{
		{Kind: "node", Name: "a", Driver: "dep", Options: rawJSON(nodeOpt{})},
		{Kind: "node", Name: "b", Driver: "dep", Options: rawJSON(nodeOpt{Dep: "a"})},
	})
	must(err)

	graph := container.Graph()
	fmt.Printf("nodes=%d edges=%d\n", len(graph.Nodes), len(graph.Edges))
	fmt.Println("dot has edge:", strings.Contains(graph.DOT(), "->"))
	fmt.Println("mermaid prefix:", strings.HasPrefix(graph.Mermaid(), "graph TD"))
}

func rawJSON(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
