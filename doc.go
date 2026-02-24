// Package resorch provides a declarative resource orchestration runtime.
//
// It offers:
// - resource definition registration with (kind, driver) and generic Definition
// - instance declarations through NodeSpec
// - startup dependency validation (missing definitions/dependencies, cycles)
// - runtime lazy build, instance caching, and singleflight deduplication
// - reverse-topological shutdown
package resorch
