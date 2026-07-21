// Package main is the entry point for the y-ai-agent-base HTTP server.
package main

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/anrror/y-ai-agent-base/internal/handler"
	"github.com/anrror/y-ai-agent-base/internal/server"
	"github.com/anrror/y-ai-agent-base/pkg/agent"
	appcfg "github.com/anrror/y-ai-agent-base/pkg/config"
	"github.com/anrror/y-ai-agent-base/pkg/pipeline"
	"github.com/anrror/y-ai-agent-base/pkg/provider"
	"github.com/anrror/y-ai-agent-base/pkg/team"
	"github.com/anrror/y-ai-agent-base/pkg/types"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// 1. Load config.
	cfg, err := appcfg.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	// 2. Build server with demo agents and a demo team.
	srv, err := server.New(cfg,
		server.WithSeed(func(reg *agent.Registry, prov provider.LLMProvider, pipe pipeline.Pipeline) error {
			return seedAgents(reg, prov, pipe)
		}),
		server.WithTeam(func(h *handler.Handler, reg *agent.Registry, prov provider.LLMProvider, pipe pipeline.Pipeline) error {
			return seedTeam(h, reg, prov, pipe)
		}),
	)
	if err != nil {
		return fmt.Errorf("server: %w", err)
	}

	// 3. Start (blocking).
	return srv.Run()
}

// ─── Demo Agent Seeds ────────────────────────────────────────────────────────

type seedDef struct {
	id       string
	model    string
	identity agent.Identity
	ocean    agent.OCEAN
	tone     string
}

func seedAgents(reg *agent.Registry, prov provider.LLMProvider, pipe pipeline.Pipeline) error {
	seeds := []seedDef{
		{
			id: "assistant", model: "gpt-4o",
			identity: agent.Identity{
				Name: "General Assistant", Role: "helpful assistant",
				Description: "A knowledgeable and courteous general-purpose assistant.",
				Tone: "friendly", Verbosity: "balanced",
				Constraints: []string{"be helpful", "stay factual", "ask for clarification when needed"},
			},
			ocean: agent.OCEAN{
				Openness: 0.9, Conscientiousness: 0.85, Extraversion: 0.5,
				Agreeableness: 0.9, Neuroticism: 0.2,
			},
			tone: "friendly and helpful",
		},
		{
			id: "coder", model: "gpt-4o",
			identity: agent.Identity{
				Name: "Code Expert", Role: "senior software engineer",
				Description: "A pragmatic senior engineer who writes clean, testable code.",
				Tone: "professional", Verbosity: "concise",
				Constraints: []string{"write tests first", "prefer readability", "no over-engineering"},
			},
			ocean: agent.OCEAN{
				Openness: 0.7, Conscientiousness: 0.95, Extraversion: 0.3,
				Agreeableness: 0.6, Neuroticism: 0.15,
			},
			tone: "professional and direct",
		},
		{
			id: "creative", model: "gpt-4o",
			identity: agent.Identity{
				Name: "Creative Writer", Role: "creative writing coach",
				Description: "An imaginative writing coach who helps with storytelling and creative expression.",
				Tone: "supportive and inspiring", Verbosity: "expressive",
				Constraints: []string{"encourage creativity", "give specific feedback", "never criticize without offering alternatives"},
			},
			ocean: agent.OCEAN{
				Openness: 1.0, Conscientiousness: 0.55, Extraversion: 0.85,
				Agreeableness: 0.8, Neuroticism: 0.4,
			},
			tone: "warm and encouraging",
		},
	}

	for _, s := range seeds {
		ac := agent.Config{
			AgentID:     s.id,
			Identity:    &s.identity,
			Personality: s.ocean,
			LLMConfig: types.ModelConfig{
				Model: s.model, Temperature: 0.7, MaxTokens: 4096,
			},
			PromptTmpl: buildPrompt(s.identity, s.tone),
			Status:     agent.StatusReady,
		}
		ac.FillDefaults()

		ag, err := ac.ToBuilder().WithProvider(prov).WithPipeline(pipe).Build()
		if err != nil {
			return fmt.Errorf("build %q: %w", s.id, err)
		}
		if err := reg.Register(ag); err != nil {
			return fmt.Errorf("register %q: %w", s.id, err)
		}
	}

	slog.Info("agents seeded", "count", reg.Count())
	return nil
}

// ─── Demo Team Seed ──────────────────────────────────────────────────────────

// seedTeam creates a multi-agent team with "coder" and "creative" as members
// under a "project-team" supervisor. The supervisor is registered in both the
// agent registry (for direct chat) and the team registry (for team APIs).
//
// The supervisor agent has delegate_to_coder and delegate_to_creative tools,
// enabling it to delegate tasks to specialized members via LLM function calling.
func seedTeam(h *handler.Handler, reg *agent.Registry, prov provider.LLMProvider, pipe pipeline.Pipeline) error {
	coder, ok := reg.Get("coder")
	if !ok {
		return fmt.Errorf("team: coder agent not found")
	}
	creative, ok := reg.Get("creative")
	if !ok {
		return fmt.Errorf("team: creative agent not found")
	}

	// Supervisor config — orchestrator that delegates to specialists.
	supervisorCfg := agent.Config{
		AgentID: "project-team",
		Identity: &agent.Identity{
			Name: "Project Supervisor", Role: "project manager",
			Description: "An orchestrator that delegates tasks to specialist agents. " +
				"Use delegate_to_coder for coding tasks and delegate_to_creative for creative tasks.",
			Tone: "professional", Verbosity: "balanced",
			Constraints: []string{
				"delegate coding tasks to the coder agent",
				"delegate creative/writing tasks to the creative agent",
				"synthesize responses from multiple agents when needed",
				"never try to do specialized work yourself — always delegate",
			},
		},
		Personality: agent.OCEAN{
			Openness: 0.8, Conscientiousness: 0.9, Extraversion: 0.5,
			Agreeableness: 0.85, Neuroticism: 0.15,
		},
		LLMConfig: types.ModelConfig{
			Model: "gpt-4o", Temperature: 0.7, MaxTokens: 4096,
		},
		PromptTmpl: "You are a Project Supervisor that orchestrates a team of specialist agents.\n" +
			"You have access to the following specialists via function calling:\n" +
			"- coder: a senior software engineer\n" +
			"- creative: a creative writing coach\n" +
			"Always delegate tasks to the appropriate specialist. Never try to do their job yourself.",
		Status: agent.StatusReady,
	}
	supervisorCfg.FillDefaults()

	t, err := team.New(supervisorCfg, prov, pipe, coder, creative)
	if err != nil {
		return fmt.Errorf("team: create: %w", err)
	}

	// Register supervisor as a normal agent (so it works with /chat/completions).
	if err := reg.Register(t.Supervisor); err != nil {
		return fmt.Errorf("team: register supervisor: %w", err)
	}

	// Register team for team API endpoints.
	if err := h.TeamRegistry.Register(t); err != nil {
		return fmt.Errorf("team: register: %w", err)
	}

	slog.Info("team seeded",
		"team", t.ID,
		"supervisor", t.Supervisor.ID(),
		"members", t.AgentIDs(),
	)
	return nil
}

func buildPrompt(id agent.Identity, tone string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are %s, a %s. %s\n", id.Name, id.Role, id.Description)
	fmt.Fprintf(&b, "Tone: %s. Verbosity: %s.\n", tone, id.Verbosity)
	if len(id.Constraints) > 0 {
		b.WriteString("Rules:\n")
		for _, c := range id.Constraints {
			fmt.Fprintf(&b, "- %s\n", c)
		}
	}
	return strings.TrimSpace(b.String())
}
