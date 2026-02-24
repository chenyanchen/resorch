package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/chenyanchen/resorch"
)

type nodeOpt struct {
	Name string `json:"name"`
	Dep  string `json:"dep"`
}

type node struct {
	Name string
}

func main() {
	reg := resorch.NewRegistry()

	resorch.MustRegister(reg, "node", "dep", resorch.Definition[nodeOpt, *node]{
		Deps: func(opt nodeOpt) ([]resorch.ID, error) {
			if opt.Dep == "" {
				return nil, nil
			}
			return []resorch.ID{{Kind: "node", Name: opt.Dep}}, nil
		},
		Build: func(_ context.Context, _ resorch.Resolver, opt nodeOpt) (*node, error) {
			return &node{Name: opt.Name}, nil
		},
		Close: func(_ context.Context, n *node) error {
			fmt.Printf("node %s closed\n", n.Name)
			return nil
		},
	})

	container, err := resorch.NewContainer(reg, []resorch.NodeSpec{
		{Kind: "node", Name: "a", Driver: "dep", Options: rawJSON(nodeOpt{Name: "a"})},
		{Kind: "node", Name: "b", Driver: "dep", Options: rawJSON(nodeOpt{Name: "b", Dep: "a"})},
	})
	must(err)

	_, err = container.Resolve(context.Background(), resorch.ID{Kind: "node", Name: "b"})
	must(err)

	must(container.Close(context.Background()))
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
