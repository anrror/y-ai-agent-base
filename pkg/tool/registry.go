package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/anrror/y-ai-agent-base/pkg/util"
)

// Sentinel errors for tool registry operations.
var (
	ErrToolNotFound    = errors.New("tool not found")
	ErrToolExists      = errors.New("tool already registered")
	ErrInvalidArgs     = errors.New("invalid tool arguments")
	ErrToolCallTimeout = errors.New("tool call timed out")
)

// Registry manages a thread-safe collection of registered tools.
type Registry struct {
	inner *util.Registry[Tool]
}

// NewRegistry creates an empty tool registry.
func NewRegistry() *Registry {
	return &Registry{inner: util.NewRegistry[Tool]()}
}

// Register adds a tool to the registry. Returns an error if a tool
// with the same name is already registered.
func (r *Registry) Register(t Tool) error {
	name := t.Name()
	err := r.inner.Register(name, t)
	if err != nil {
		if errors.Is(err, util.ErrAlreadyExists) {
			return fmt.Errorf("%w: %s", ErrToolExists, name)
		}
		return fmt.Errorf("registry: %w", err)
	}
	return nil
}

// Get returns a tool by name. The second return value indicates
// whether the tool was found.
func (r *Registry) Get(name string) (Tool, bool) {
	return r.inner.Get(name)
}

// List returns all registered tools as a slice.
func (r *Registry) List() []Tool {
	return r.inner.List()
}

// Call looks up a tool by name and executes it with the given arguments.
// It respects the context deadline.
func (r *Registry) Call(ctx context.Context, name string, args json.RawMessage) (string, error) {
	t, ok := r.Get(name)
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrToolNotFound, name)
	}

	if err := ctx.Err(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return "", fmt.Errorf("%w: %s", ErrToolCallTimeout, name)
		}
		return "", fmt.Errorf("tool context: %w", err)
	}

	result, err := t.Execute(ctx, args)
	if err != nil {
		return "", fmt.Errorf("tool execute: %w", err)
	}
	return result, nil
}
