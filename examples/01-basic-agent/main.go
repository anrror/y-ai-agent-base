// Example 01: Minimal Agent
//
// This example demonstrates the most basic agent setup:
//   1. Create an OpenAI-compatible provider
//   2. Build a pipeline around it
//   3. Construct an agent and run a simple chat
//
// Run with:
//   export OPENAI_API_KEY=sk-...
//   go run ./examples/01-basic-agent/
//
// Without an API key, the agent is still built and validated;
// the actual API call is skipped.

package main

import (
	"context"
	"fmt"
	"os"

	"github.com/anrror/y-ai-agent-base/pkg/agent"
	"github.com/anrror/y-ai-agent-base/pkg/pipeline"
	"github.com/anrror/y-ai-agent-base/pkg/provider"
	"github.com/anrror/y-ai-agent-base/pkg/provider/openai"
	"github.com/anrror/y-ai-agent-base/pkg/types"
)

func main() {
	// #1 Read API key from environment (or skip the live call)
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		fmt.Println("INFO: OPENAI_API_KEY not set. The agent will be built but no API call made.")
		fmt.Println("      Set it to run a live demo:  export OPENAI_API_KEY=sk-...")
	}

	// #2 Create an OpenAI-compatible provider.
	//    Each role (chat, embedding, guard) gets its own *provider.ProviderConfig.
	//    Pass nil for roles you do not need — here only chat is configured.
	provider := openai.NewOpenAIProvider(&provider.ProviderConfig{
		Type:    "openai",
		APIKey:  apiKey,
		BaseURL: "https://api.openai.com/v1",
		Model:   "gpt-4o-mini",
	})

	// #3 Create a processing pipeline wired to the provider.
	//    pipeline.New accepts optional middleware; for this basic example
	//    we pass none.
	p := pipeline.New(provider)

	// #4 Configure and build the agent.
	//    The builder pattern enforces that AgentID, Provider, and Pipeline
	//    are all set before Build() succeeds.
	cfg := agent.Config{
		AgentID: "basic-agent",
		LLMConfig: types.ModelConfig{
			Model: "gpt-4o-mini",
		},
	}

	a, err := cfg.ToBuilder().
		WithProvider(provider).
		WithPipeline(p).
		Build()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: agent build failed: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = a.Close() }()

	fmt.Printf("Agent %q built successfully.\n", a.ID())

	// #5 Run a simple synchronous chat if we have an API key.
	if apiKey == "" {
		return
	}

	ctx := context.Background()
	output, err := a.Run(ctx, types.ChatInput{
		Messages: []types.Message{
			{Role: "user", Content: "Hello! In one sentence, what can you do?"},
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: chat failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Response: %s\n", output.Content)
}
