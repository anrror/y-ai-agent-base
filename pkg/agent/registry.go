package agent

import (
	"errors"
	"fmt"

	"github.com/anrror/y-ai-agent-base/pkg/util"
)

// Registry stores and retrieves Agent instances. It is safe for
// concurrent use.
type Registry struct {
	inner *util.Registry[*Agent]
}

// NewRegistry creates an empty agent registry.
func NewRegistry() *Registry {
	return &Registry{inner: util.NewRegistry[*Agent]()}
}

// Register adds an agent to the registry. Returns an error when an
// agent with the same ID is already registered or when the agent is nil.
func (r *Registry) Register(agent *Agent) error {
	if agent == nil {
		return fmt.Errorf("agent: cannot register nil agent")
	}

	err := r.inner.Register(agent.ID(), agent)
	if err != nil {
		if errors.Is(err, util.ErrAlreadyExists) {
			return fmt.Errorf("agent: %q is already registered", agent.ID())
		}
		return fmt.Errorf("registry: %w", err)
	}
	return nil
}

// Get retrieves an agent by ID. The second return value indicates
// whether the agent was found.
func (r *Registry) Get(id string) (*Agent, bool) {
	return r.inner.Get(id)
}

// List returns all registered agents as a slice.
func (r *Registry) List() []*Agent {
	return r.inner.List()
}

// Delete removes an agent from the registry by ID. Returns an error
// when no agent with the given ID is found.
func (r *Registry) Delete(id string) error {
	err := r.inner.Delete(id)
	if err != nil {
		if errors.Is(err, util.ErrNotFound) {
			return fmt.Errorf("agent: %q not found", id)
		}
		return fmt.Errorf("registry: %w", err)
	}
	return nil
}

// Count returns the number of registered agents.
func (r *Registry) Count() int {
	return r.inner.Count()
}
