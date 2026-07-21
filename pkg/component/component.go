// Package component defines the lifecycle interface for all pluggable
// subsystems (reasoning, compressor, cache, scheduler, edge, driver,
// inference) that attach to an Agent. Consumer extensions (emotion,
// personality evolution, etc.) plug in via the same Component contract.
//
// Architecture
//
// Component is a superset of agent.Extension:
//   - Extension: ID() + Close()
//   - Component: ID() + Init(*InitContext) error + Close()
//
// During Agent.Build(), every Extension that also implements Component is
// collected, sorted by Priority, and initialized via Init(). This two-phase
// lifecycle allows components to inject middleware, discover each other,
// and wire up cross-component dependencies at build time, before the first
// Chat() call.
//
// Priority tiers (gap of 100 between tiers allows future insertion):
//
//	PriorityObservability  (-200)  — logging, recovery, tracing
//	PriorityEarly           (-100)  — cache hit
//	PriorityNormal          (0)     — safety, identity, memory
//	PriorityLate            (100)   — compression
//	PriorityTerminal        (200)   — driver (outermost wrapper)
package component

import "github.com/anrror/y-ai-agent-base/pkg/pipeline"

// Priority controls middleware injection order. Lower values wrap closer to
// the terminal handler (run first on the way in, last on the way out).
type Priority int

const (
	PriorityObservability Priority = -200
	PriorityEarly         Priority = -100
	PriorityNormal        Priority = 0
	PriorityLate          Priority = 100
	PriorityTerminal      Priority = 200
)

// Component is the lifecycle interface for pluggable subsystems.
//
// Implementations MUST be safe for uninitialized use (Init may be called
// zero times if the component is registered but Build() fails validation).
// All exported methods besides ID / Init / Close should document whether
// they require Init to have been called first.
type Component interface {
	// ID returns a unique identifier for this component, e.g. "cache.redis".
	// Must be non-empty. Used as the map key in Agent.Extensions.
	ID() string

	// Init is called during Agent.Build() after the Agent struct and
	// pipeline are fully assembled. Components use Init to:
	//   - inject middleware via ctx.Pipeline.Use()
	//   - register post-process hooks via ctx.Pipeline.OnPostProcess()
	//   - discover sibling components via ctx.Lookup()
	Init(ctx *InitContext) error

	// Close releases resources held by the component.
	// Called during Agent.Close(). Must be idempotent.
	Close() error
}

// PriorityProvider is an optional sub-interface of Component.
// When a Component also implements PriorityProvider, Build() uses
// this priority for middleware ordering. Otherwise a default of 0
// (PriorityNormal) is assumed.
type PriorityProvider interface {
	Priority() Priority
}

// CategorisedComponent is an optional sub-interface of Component.
// When a Component implements CategorisedComponent, the Registry groups
// it alongside all other components with the same category.
//
// This enables multi-instance orchestration: e.g. multiple cache backends
// can all register with Category("cache"), and a compound orchestrator
// can discover and invoke all of them via InitContext.LookupAll().
//
// Well-known categories used by the built-in system:
//
//	"reasoning"            — reasoning paradigm engines
//	"compressor"           — context compressors
//	"cache"                — inference caches
//	"scheduler"            — model schedulers
//	"edge"                 — edge-cloud managers
//	"driver"               — LLM drivers (singleton)
//	"inference"            — inference routers (singleton)
//
// Consumer extensions define their own categories (e.g. "memory.reranker",
// "personality.evolution") — they are not framework built-ins.
type CategorisedComponent interface {
	Component
	// Category returns the functional category identifier.
	// Components sharing the same category are considered interchangeable
	// or composable (the orchestration semantics are category-specific).
	Category() string
}

// InitContext is handed to Component.Init(). It provides limited,
// safe access to the agent's internals — specifically the pipeline
// for middleware injection and lookup functions for sibling components.
//
// The fields are intentionally minimal to prevent components from
// depending on the agent package at compile time (avoiding circular
// imports).
type InitContext struct {
	// Pipeline exposes Use() and OnPostProcess() for middleware and
	// post-hook registration. Components are free to call these during
	// Init(); the pipeline instance is owned by the Agent and will be
	// ready for use.
	Pipeline pipeline.Pipeline

	// Lookup returns another registered Component by ID, or nil when
	// no component with that ID exists. This is the only mechanism for
	// cross-component dependency discovery (e.g. Driver looking up the
	// inference router).
	Lookup func(id string) Component

	// LookupAll returns every Component whose Category matches the given
	// string. The slice is empty when none match.
	//
	// This is the primary mechanism for multi-instance orchestration:
	// an orchestrator component calls LookupAll("cache") to discover
	// all registered cache backends, then invokes each in sequence or
	// in parallel and aggregates their results.
	LookupAll func(category string) []Component
}
