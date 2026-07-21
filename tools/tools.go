//go:build tools

// Package tools tracks build-time tool dependencies.
// This ensures tool versions are pinned in go.mod and go.sum.
package tools

import (
	// Test framework
	_ "github.com/stretchr/testify"
)
