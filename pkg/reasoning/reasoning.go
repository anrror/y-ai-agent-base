// Package reasoning provides pluggable inference paradigms (Direct, CoT,
// ReAct, ToT, PlanExecute). Each paradigm implements the Engine interface;
// built-in implementations are included and can be replaced by external
// registrations.
package reasoning

import "context"

// Paradigm identifies a reasoning strategy.
type Paradigm string

const (
	ParadigmDirect      Paradigm = "direct"
	ParadigmCoT         Paradigm = "cot"
	ParadigmReAct       Paradigm = "react"
	ParadigmToT         Paradigm = "tot"
	ParadigmPlanExecute Paradigm = "plan_execute"
)

// Request carries the information needed to perform reasoning.
type Request struct {
	SystemPrompt string
	Messages     []Message
	Tools        []Tool
	MaxTokens    int
	// ModelFunc is a callback that sends messages to an LLM and returns
	// the text response. CoT/ReAct/ToT reasoners call this iteratively;
	// Direct passes it through untouched.
	ModelFunc func(ctx context.Context, system string, msgs []Message) (string, error)

	// ParadigmConfig holds paradigm-specific parameters (e.g. number of
	// branches for ToT, max ReAct iterations).
	ParadigmConfig map[string]any
}

// Message is a lightweight type for reasoning — intentionally decoupled
// from types.Message to keep this package dependency-free.
type Message struct {
	Role    string // "system" | "user" | "assistant" | "tool"
	Content string
	Name    string // optional tool name
}

// Tool is the minimal tool descriptor needed by reasoners.
type Tool struct {
	Name        string
	Description string
}

// Step records one atomic reasoning unit (primarily used by ReAct and ToT).
type Step struct {
	Type    string // "thought" | "action" | "observation" | "evaluation"
	Content string
}

// Result captures the output of a reasoning pass.
type Result struct {
	Content  string // final answer
	Thinking string // chain-of-thought trace (if supported, may be empty)
	Paradigm Paradigm
	Steps    []Step
}

// Engine performs reasoning using a specific paradigm.
type Engine interface {
	// Reason applies the paradigm to the request and returns the result.
	Reason(ctx context.Context, request *Request) (*Result, error)

	// Paradigm identifies which strategy this engine implements.
	Paradigm() Paradigm
}
