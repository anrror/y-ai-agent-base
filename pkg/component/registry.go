package component

import "sort"

// Registry manages the lifecycle of registered Components.
// It is used internally by Agent.Build() to collect, sort, and initialize
// components.
type Registry struct {
	components map[string]Component
}

// NewRegistry creates an empty component registry.
func NewRegistry() *Registry {
	return &Registry{components: make(map[string]Component)}
}

// Register adds a component. If a component with the same ID already
// exists the old one is replaced (Close() is NOT called on the old one —
// replacements are the caller's responsibility).
func (r *Registry) Register(c Component) {
	r.components[c.ID()] = c
}

// Get returns a component by ID, or nil if not found.
func (r *Registry) Get(id string) Component {
	return r.components[id]
}

// List returns all registered components, ordered by priority (ascending).
func (r *Registry) List() []Component {
	out := make([]Component, 0, len(r.components))
	for _, c := range r.components {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		return priorityOf(out[i]) < priorityOf(out[j])
	})
	return out
}

// ListByCategory returns all components in the given category, ordered by
// priority (ascending). Components that don't implement CategorisedComponent
// are excluded.
func (r *Registry) ListByCategory(category string) []Component {
	var out []Component
	for _, c := range r.components {
		if cat, ok := c.(CategorisedComponent); ok && cat.Category() == category {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return priorityOf(out[i]) < priorityOf(out[j])
	})
	return out
}

// Categories returns all distinct category strings found among registered
// components.
func (r *Registry) Categories() []string {
	seen := make(map[string]struct{})
	for _, c := range r.components {
		if cat, ok := c.(CategorisedComponent); ok {
			seen[cat.Category()] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for cat := range seen {
		out = append(out, cat)
	}
	sort.Strings(out)
	return out
}

// Len returns the number of registered components.
func (r *Registry) Len() int { return len(r.components) }

// priorityOf extracts the Priority from a Component, defaulting to
// PriorityNormal (0) when the component does not implement PriorityProvider.
func priorityOf(c Component) Priority {
	if p, ok := c.(PriorityProvider); ok {
		return p.Priority()
	}
	return PriorityNormal
}
