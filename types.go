package resorch

import (
	"context"
	"encoding/json"
)

// ID is the unique identifier of a resource instance.
// Kind identifies the resource type (for example, redis or pipeline).
// Name identifies one instance within the same kind.
type ID struct {
	Kind string `json:"kind" yaml:"kind"`
	Name string `json:"name" yaml:"name"`
}

func (id ID) String() string {
	return id.Kind + "/" + id.Name
}

// NodeSpec is the framework-agnostic declaration of a resource instance.
// Any config layer that can map into this struct can integrate with resorch.
type NodeSpec struct {
	Kind    string          `json:"kind" yaml:"kind"`
	Name    string          `json:"name" yaml:"name"`
	Driver  string          `json:"driver" yaml:"driver"`
	Options json.RawMessage `json:"options,omitempty" yaml:"options,omitempty"`
}

func (s NodeSpec) ID() ID {
	return ID{Kind: s.Kind, Name: s.Name}
}

// Resolver provides runtime dependency resolution.
type Resolver interface {
	Resolve(ctx context.Context, id ID) (any, error)
}

// Definition describes the build lifecycle of one (kind, driver).
//
// Decode converts raw options into Opt. Defaults to JSON decoding.
// Deps declares static dependencies for startup graph validation/building.
// Build constructs the runtime instance and must be provided.
// Close is an optional shutdown hook. If omitted, io.Closer is used when possible.
type Definition[Opt any, Out any] struct {
	Decode func(raw json.RawMessage) (Opt, error)
	Deps   func(opt Opt) ([]ID, error)
	Build  func(ctx context.Context, r Resolver, opt Opt) (Out, error)
	Close  func(ctx context.Context, out Out) error
}
