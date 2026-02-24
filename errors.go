package resorch

import (
	"fmt"
	"strings"
)

// DefinitionNotFoundError means a (kind, driver) definition is not registered.
type DefinitionNotFoundError struct {
	Kind   string
	Driver string
}

func (e DefinitionNotFoundError) Error() string {
	return fmt.Sprintf("resource definition not found: kind=%q driver=%q", e.Kind, e.Driver)
}

// DuplicateNodeError means the same ID appears more than once in specs.
type DuplicateNodeError struct {
	ID ID
}

func (e DuplicateNodeError) Error() string {
	return fmt.Sprintf("duplicate resource node: %s", e.ID.String())
}

// NodeNotFoundError means resolving a node that is not declared.
type NodeNotFoundError struct {
	ID ID
}

func (e NodeNotFoundError) Error() string {
	return fmt.Sprintf("resource node not found: %s", e.ID.String())
}

// DependencyNotFoundError means a declared dependency node does not exist.
type DependencyNotFoundError struct {
	From ID
	To   ID
}

func (e DependencyNotFoundError) Error() string {
	return fmt.Sprintf("resource dependency not found: %s -> %s", e.From.String(), e.To.String())
}

// CycleDetectedError means there is a dependency cycle in the graph.
type CycleDetectedError struct {
	Path []ID
}

func (e CycleDetectedError) Error() string {
	if len(e.Path) == 0 {
		return "resource dependency cycle detected"
	}
	parts := make([]string, len(e.Path))
	for i := range e.Path {
		parts[i] = e.Path[i].String()
	}
	return "resource dependency cycle detected: " + strings.Join(parts, " -> ")
}

// TypeMismatchError means ResolveAs[T] failed to cast the resolved instance to T.
type TypeMismatchError struct {
	ID       ID
	Expected string
	Actual   string
}

func (e TypeMismatchError) Error() string {
	return fmt.Sprintf("resource type mismatch for %s: expected=%s actual=%s",
		e.ID.String(), e.Expected, e.Actual)
}
