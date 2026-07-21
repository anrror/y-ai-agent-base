package safety

import (
	"context"
	"strings"
)

// MockGuard is a GuardProvider implementation for testing. It returns
// (true, nil) for safe text and (false, nil) for text containing "bad".
// No external LLM or service is required.
type MockGuard struct {
	// AllowAll causes every check to return (true, nil) regardless of input.
	AllowAll bool
	// BlockAll causes every check to return (false, nil) regardless of input.
	BlockAll bool
}

// Check implements provider.GuardProvider.
func (m *MockGuard) Check(_ context.Context, text string) (bool, error) {
	if m.AllowAll {
		return true, nil
	}
	if m.BlockAll {
		return false, nil
	}
	// Default: text containing "bad" is unsafe.
	if containsBad(text) {
		return false, nil
	}
	return true, nil
}

// containsBad returns true when text contains the substring "bad"
// (case-sensitive).
func containsBad(text string) bool {
	return strings.Contains(text, "bad")
}
