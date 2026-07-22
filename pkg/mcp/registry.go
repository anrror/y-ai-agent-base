package mcp

import (
	"fmt"
	"sync"
)

// Registry holds a collection of named MCP Server instances.
//
// The host system populates the Registry with all available MCP servers
// at startup. Agents reference servers by name, and sessions can override
// which servers are active per conversation.
//
// Registry is goroutine-safe: Add and Remove may be called concurrently
// with List, Get, and tool resolution.
type Registry struct {
	mu      sync.RWMutex
	servers map[string]*Server
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		servers: make(map[string]*Server),
	}
}

// Add registers an MCP server. Returns an error if a server with the same
// name already exists.
func (r *Registry) Add(s *Server) error {
	if s == nil {
		return fmt.Errorf("mcp: cannot add nil server")
	}
	if err := s.Validate(); err != nil {
		return fmt.Errorf("mcp: invalid server: %w", err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.servers[s.Name]; exists {
		return fmt.Errorf("mcp: server %q already registered", s.Name)
	}
	r.servers[s.Name] = s
	return nil
}

// Get returns the server with the given name, or nil if not found.
func (r *Registry) Get(name string) *Server {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.servers[name]
}

// List returns all registered servers.
func (r *Registry) List() []*Server {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]*Server, 0, len(r.servers))
	for _, s := range r.servers {
		out = append(out, s)
	}
	return out
}

// ServerNames returns the names of all registered servers.
func (r *Registry) ServerNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]string, 0, len(r.servers))
	for name := range r.servers {
		out = append(out, name)
	}
	return out
}

// Remove unregisters a server by name. Returns true if the server existed.
// The server's Close() method is NOT called — the caller should close
// it separately if needed.
func (r *Registry) Remove(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	_, exists := r.servers[name]
	delete(r.servers, name)
	return exists
}

// Len returns the number of registered servers.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.servers)
}

// Close calls Close() on all registered servers and clears the registry.
func (r *Registry) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	var errs []string
	for name, s := range r.servers {
		if err := s.Close(); err != nil {
			errs = append(errs, name+": "+err.Error())
		}
	}
	r.servers = make(map[string]*Server)

	if len(errs) > 0 {
		return fmt.Errorf("mcp: registry close errors: %s", joinStrings(errs, "; "))
	}
	return nil
}

func joinStrings(parts []string, sep string) string {
	if len(parts) == 0 {
		return ""
	}
	result := parts[0]
	for _, p := range parts[1:] {
		result += sep + p
	}
	return result
}
