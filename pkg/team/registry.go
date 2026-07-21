package team

import (
	"errors"
	"fmt"

	"github.com/anrror/y-ai-agent-base/pkg/util"
)

// Registry stores and retrieves Team instances. Safe for concurrent use.
type Registry struct {
	inner *util.Registry[*Team]
}

// NewRegistry creates an empty team registry.
func NewRegistry() *Registry {
	return &Registry{inner: util.NewRegistry[*Team]()}
}

// Register adds a team to the registry by its ID. Returns an error when
// a team with the same ID already exists or when the team is nil.
func (r *Registry) Register(t *Team) error {
	if t == nil {
		return fmt.Errorf("team: cannot register nil team")
	}
	err := r.inner.Register(t.ID, t)
	if err != nil {
		if errors.Is(err, util.ErrAlreadyExists) {
			return fmt.Errorf("team: %q is already registered", t.ID)
		}
		return fmt.Errorf("team registry: %w", err)
	}
	return nil
}

// Get retrieves a team by ID. The second return value indicates whether
// the team was found.
func (r *Registry) Get(id string) (*Team, bool) {
	return r.inner.Get(id)
}

// List returns all registered teams.
func (r *Registry) List() []*Team {
	return r.inner.List()
}

// Delete removes a team from the registry by ID. Error when not found.
func (r *Registry) Delete(id string) error {
	err := r.inner.Delete(id)
	if err != nil {
		return fmt.Errorf("team: %q not found", id)
	}
	return nil
}

// Count returns the number of registered teams.
func (r *Registry) Count() int {
	return r.inner.Count()
}
