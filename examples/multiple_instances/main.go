package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/chenyanchen/resorch"
)

type valueOpt struct {
	Value string `json:"value"`
}

type joinOpt struct {
	Left  string `json:"left"`
	Right string `json:"right"`
}

func main() {
	reg := resorch.NewRegistry()
	resorch.MustRegister(reg, "kv", "mem", resorch.Definition[valueOpt, string]{
		Build: func(_ context.Context, _ resorch.Resolver, opt valueOpt) (string, error) {
			return opt.Value, nil
		},
	})
	resorch.MustRegister(reg, "join", "pair", resorch.Definition[joinOpt, string]{
		Deps: func(opt joinOpt) ([]resorch.ID, error) {
			return []resorch.ID{
				{Kind: "kv", Name: opt.Left},
				{Kind: "kv", Name: opt.Right},
			}, nil
		},
		Build: func(ctx context.Context, r resorch.Resolver, opt joinOpt) (string, error) {
			left, err := resorch.ResolveAs[string](ctx, r, resorch.ID{Kind: "kv", Name: opt.Left})
			if err != nil {
				return "", err
			}
			right, err := resorch.ResolveAs[string](ctx, r, resorch.ID{Kind: "kv", Name: opt.Right})
			if err != nil {
				return "", err
			}
			return left + "+" + right, nil
		},
	})

	container, err := resorch.NewContainer(reg, []resorch.NodeSpec{
		{Kind: "kv", Name: "left", Driver: "mem", Options: rawJSON(valueOpt{Value: "L"})},
		{Kind: "kv", Name: "right", Driver: "mem", Options: rawJSON(valueOpt{Value: "R"})},
		{Kind: "join", Name: "combined", Driver: "pair", Options: rawJSON(joinOpt{Left: "left", Right: "right"})},
	})
	must(err)

	combined, err := resorch.ResolveAs[string](context.Background(), container, resorch.ID{
		Kind: "join",
		Name: "combined",
	})
	must(err)

	fmt.Println("combined value:", combined)
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
