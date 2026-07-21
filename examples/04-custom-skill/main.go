// Example 04: Custom Skills — Time & Weather
//
// This example demonstrates the skills system:
//   1. Create a Time skill with a custom description and instructions
//   2. Create a Weather skill with tags for automatic query matching
//   3. Register them on a skills.Registry
//   4. Attach them to the agent via WithSkills
//   5. Show skill matching against sample user queries
//   6. Demonstrate that skill tools are auto-merged into the agent
//
// Run with:
//   go run ./examples/04-custom-skill/
//
// No API key required — this example focuses on skill mechanics.

package main

import (
	"context"
	"fmt"
	"os"

	"github.com/anrror/y-ai-agent-base/pkg/agent"
	"github.com/anrror/y-ai-agent-base/pkg/pipeline"
	"github.com/anrror/y-ai-agent-base/pkg/provider"
	"github.com/anrror/y-ai-agent-base/pkg/provider/openai"
	"github.com/anrror/y-ai-agent-base/pkg/skills"
	"github.com/anrror/y-ai-agent-base/pkg/skills/builtin"
	"github.com/anrror/y-ai-agent-base/pkg/tool"
	"github.com/anrror/y-ai-agent-base/pkg/types"
)

func main() {
	// #1 Create a Time skill using the functional builder.
	//
	//    Each skill bundles: metadata, instructions for the LLM,
	//    tools it provides, tags for query matching, and a match function.
	timeSkill := skills.NewSkill(
		"time-skill",
		skills.WithDescription("Provides the current date and time for any timezone."),
		skills.WithInstructions(
			"When the user asks about the current time or date, use the get_current_time tool.",
		),
		skills.WithTools(tool.TimeTool()),
		skills.WithTags("time", "date", "clock", "now", "today", "timezone"),
		skills.WithCategory("utility"),
		skills.WithVersion("1.0.0"),
		skills.WithAuthor("examples"),
	)

	// #2 Create a Weather skill with different tags for matching.
	weatherSkill := skills.NewSkill(
		"weather-skill",
		skills.WithDescription("Provides weather forecasts and current conditions."),
		skills.WithInstructions(
			"When the user asks about weather, use the get_weather tool with the city name.",
		),
		skills.WithTools(tool.WeatherTool()),
		skills.WithTags("weather", "temperature", "forecast", "rain", "sunny", "climate"),
		skills.WithCategory("utility"),
		skills.WithVersion("1.0.0"),
		skills.WithAuthor("examples"),
	)

	// #3 Also load the built-in Echo skill for comparison.
	echoSkill := builtin.EchoSkill()

	// #4 Register skills in a standalone registry and test matching.
	reg := skills.NewRegistry()
	if err := reg.Register(timeSkill); err != nil {
		fmt.Fprintf(os.Stderr, "register time skill: %v\n", err)
	}
	if err := reg.Register(weatherSkill); err != nil {
		fmt.Fprintf(os.Stderr, "register weather skill: %v\n", err)
	}
	if err := reg.Register(echoSkill); err != nil {
		fmt.Fprintf(os.Stderr, "register echo skill: %v\n", err)
	}

	fmt.Printf("Registry has %d skill(s).\n\n", reg.Count())

	// #5 Demonstrate skill matching: which skill(s) match each query?
	queries := []string{
		"What time is it in Tokyo?",
		"Will it rain tomorrow in London?",
		"Tell me today's date.",
		"Echo this back: hello world",
		"Write a poem about clouds.",
	}

	fmt.Println("=== Skill matching against sample queries ===")
	for _, q := range queries {
		results := reg.Match(context.Background(), q)
		skills.SortMatchResults(results)
		fmt.Printf("Query: %q\n", q)
		for _, r := range results {
			fmt.Printf("  → %s (score: %.2f)\n", r.Skill.Name(), r.Score)
		}
		if len(results) == 0 {
			fmt.Println("  → no matching skill")
		}
		fmt.Println()
	}

	// #6 Attach skills to the agent.
	//
	//    When Build() is called, each skill's Tools() are automatically
	//    extracted and merged into agent.Tools. The skill Instructions()
	//    can be injected into the system prompt.
	provider := openai.NewOpenAIProvider(&provider.ProviderConfig{
		Type:    "openai",
		APIKey:  os.Getenv("OPENAI_API_KEY"),
		BaseURL: "https://api.openai.com/v1",
		Model:   "gpt-4o-mini",
	})
	p := pipeline.New(provider)

	cfg := agent.Config{
		AgentID: "skills-agent",
		LLMConfig: types.ModelConfig{
			Model: "gpt-4o-mini",
		},
	}

	a, err := cfg.ToBuilder().
		WithProvider(provider).
		WithPipeline(p).
		WithSkills(timeSkill, weatherSkill). // ← skills attached here
		Build()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: build failed: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = a.Close() }()

	// #7 Inspect the agent: skills + auto-merged tools.
	fmt.Printf("=== Agent %q ===\n", a.ID())
	fmt.Printf("Skills (%d):\n", len(a.Skills))
	for _, s := range a.Skills {
		fmt.Printf("  - %s: %s (tools: %d)\n",
			s.Name(), s.Description(), len(s.Tools()))
	}

	fmt.Printf("\nAuto-merged tools (%d):\n", len(a.Tools))
	for _, t := range a.Tools {
		fmt.Printf("  - %s: %s\n", t.Name(), t.Description())
	}

	fmt.Println("\nSkills example complete.")
}
