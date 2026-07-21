package skills

import (
	"context"
	"errors"
	"fmt"

	"github.com/anrror/y-ai-agent-base/pkg/util"
)

// Sentinel errors for skill registry operations.
var (
	ErrSkillNotFound = errors.New("skill not found")
	ErrSkillExists   = errors.New("skill already registered")
)

// Registry manages a thread-safe collection of skills with
// query-based matching.
type Registry struct {
	inner *util.Registry[Skill]
}

// NewRegistry creates an empty skill registry.
func NewRegistry() *Registry {
	return &Registry{inner: util.NewRegistry[Skill]()}
}

// Register adds a skill to the registry. Returns ErrSkillExists if a
// skill with the same name is already registered.
func (r *Registry) Register(s Skill) error {
	name := s.Name()
	err := r.inner.Register(name, s)
	if err != nil {
		if errors.Is(err, util.ErrAlreadyExists) {
			return fmt.Errorf("%w: %s", ErrSkillExists, name)
		}
		return fmt.Errorf("registry: %w", err)
	}
	return nil
}

// Get returns a skill by name. The second return value indicates
// whether the skill was found.
func (r *Registry) Get(name string) (Skill, bool) {
	return r.inner.Get(name)
}

// List returns all registered skills.
func (r *Registry) List() []Skill {
	return r.inner.List()
}

// Match scores every registered skill against the query and returns
// results with a score > 0, sorted by score descending.
func (r *Registry) Match(ctx context.Context, query string) []MatchResult {
	all := r.List()
	results := make([]MatchResult, 0, len(all))
	for _, s := range all {
		score := s.Match(ctx, query)
		if score > 0 {
			results = append(results, MatchResult{Skill: s, Score: score})
		}
	}
	SortMatchResults(results)
	return results
}

// Unregister removes a skill by name. Returns ErrSkillNotFound if
// the name is not registered.
func (r *Registry) Unregister(name string) error {
	err := r.inner.Delete(name)
	if err != nil {
		if errors.Is(err, util.ErrNotFound) {
			return fmt.Errorf("%w: %s", ErrSkillNotFound, name)
		}
		return fmt.Errorf("registry: %w", err)
	}
	return nil
}

// Count returns the number of registered skills.
func (r *Registry) Count() int {
	return r.inner.Count()
}
