// Package reload provides experimental hot-reload orchestration for resorch.
//
// Reconciler is the core type and performs:
// 1. build next container from new specs
// 2. reuse unchanged instances
// 3. prewarm rebuilt instances
// 4. atomically swap current container
// 5. close expired instances from old container in reverse-topological order
//
// This package is EXPERIMENTAL and its API may change before v1.
package reload
