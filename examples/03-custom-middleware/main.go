// Example 03: Custom Middleware — Timing & Logging Pipeline
//
// This example demonstrates middleware composition:
//   1. Write a TimingMiddleware that measures handler duration
//   2. Compose it with the built-in Logging middleware
//   3. Attach both to the pipeline
//   4. Show the onion execution order (outer → inner → handler)
//
// Run with:
//   export OPENAI_API_KEY=sk-...
//   go run ./examples/03-custom-middleware/
//
// Without an API key, the middleware still runs against an empty handler
// (the pipeline short-circuits on provider error, but middleware pre/post
//  logic is still visible).

package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/anrror/y-ai-agent-base/pkg/agent"
	"github.com/anrror/y-ai-agent-base/pkg/pipeline"
	"github.com/anrror/y-ai-agent-base/pkg/provider"
	"github.com/anrror/y-ai-agent-base/pkg/provider/openai"
	"github.com/anrror/y-ai-agent-base/pkg/types"
)

// #1 TimingMiddleware measures how long the downstream handler takes.
//
//	It records time before calling next(), then logs the elapsed duration
//	after next() returns — even if next() returned an error.
func TimingMiddleware(logger func(format string, args ...any)) pipeline.Middleware {
	return func(next pipeline.Handler) pipeline.Handler {
		return func(ctx context.Context, input *types.ChatInput, output *types.ChatOutput) error {
			start := time.Now()
			err := next(ctx, input, output)
			elapsed := time.Since(start)

			// Log regardless of success/failure.
			if err != nil {
				logger("[timing] handler failed after %v: %v", elapsed, err)
			} else {
				logger("[timing] handler completed in %v, output len=%d", elapsed, len(output.Content))
			}

			return err
		}
	}
}

func main() {
	// #2 Set up provider and pipeline with TWO middleware layers.
	//    Execution order (outermost first):
	//      TimingMiddleware (pre)
	//        → pipeline.Logging (pre)
	//          → provider.Chat (terminal handler)
	//        ← pipeline.Logging (post)
	//      ← TimingMiddleware (post)
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		fmt.Println("INFO: OPENAI_API_KEY not set. The pipeline will be built but the API call will fail.")
		fmt.Println("      Set it:  export OPENAI_API_KEY=sk-...")
	}

	provider := openai.NewOpenAIProvider(&provider.ProviderConfig{
		Type:    "openai",
		APIKey:  apiKey,
		BaseURL: "https://api.openai.com/v1",
		Model:   "gpt-4o-mini",
	})

	// #3 Compose middleware at pipeline construction time.
	//    The constructor accepts variadic middleware; first argument
	//    wraps outermost.
	p := pipeline.New(
		provider,
		TimingMiddleware(log.Printf), // custom — outermost
		pipeline.Logging(func(f string, a ...any) { // built-in — middle layer
			fmt.Printf("[pipeline] "+f+"\n", a...)
		}),
	)

	// #4 You can also add middleware later via p.Use().
	//    p.Use(anotherMiddleware)

	// #5 Build the agent as usual.
	cfg := agent.Config{
		AgentID: "middleware-agent",
		LLMConfig: types.ModelConfig{
			Model: "gpt-4o-mini",
		},
	}

	a, err := cfg.ToBuilder().
		WithProvider(provider).
		WithPipeline(p).
		Build()
	if err != nil {
		log.Fatalf("build failed: %v", err)
	}
	defer func() { _ = a.Close() }()

	fmt.Printf("Agent %q built with a timing+logging pipeline.\n\n", a.ID())

	if apiKey == "" {
		fmt.Println("Skipping live call (no API key).")
		return
	}

	// #6 Run a chat — both middleware layers will log their activity.
	fmt.Println("=== Running chat (watch middleware logs) ===")
	ctx := context.Background()
	output, err := a.Run(ctx, types.ChatInput{
		Messages: []types.Message{
			{Role: "user", Content: "Say 'hello' in exactly one word."},
		},
	})
	if err != nil {
		log.Fatalf("run failed: %v", err)
	}

	fmt.Printf("\n=== Final response ===\n%s\n", output.Content)
}
