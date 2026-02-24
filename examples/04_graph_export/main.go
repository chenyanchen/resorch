package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"

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

	dot := graph.DOT()
	mermaid := graph.Mermaid()

	must(os.WriteFile("graph.dot", []byte(dot), 0o644))
	must(os.WriteFile("graph.mmd", []byte(mermaid), 0o644))
	fmt.Println("wrote graph.dot")
	fmt.Println("wrote graph.mmd")

	dotPath, lookErr := exec.LookPath("dot")
	if lookErr != nil {
		fmt.Println("graphviz 'dot' not found; skip svg rendering")
		fmt.Println("install graphviz and run: dot -Tsvg graph.dot -o graph.svg")
		return
	}

	cmd := exec.Command(dotPath, "-Tsvg", "graph.dot", "-o", "graph.svg")
	if err := cmd.Run(); err != nil {
		fmt.Printf("dot found but failed to render graph.svg: %v\n", err)
		fmt.Println("you can retry manually: dot -Tsvg graph.dot -o graph.svg")
		return
	}
	fmt.Println("wrote graph.svg")
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
