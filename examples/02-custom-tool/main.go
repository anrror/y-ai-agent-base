// Example 02: Custom Tool — Calculator
//
// This example demonstrates creating and registering a custom tool:
//   1. Define a Calculator tool (add / multiply) using tool.FromFunction
//   2. Declare its parameter schema with the ParamSchema builder
//   3. Register it on the agent via WithTools
//   4. Call the tool directly (without LLM) to prove it works
//   5. Show the JSON schema the tool exposes to the LLM
//
// Run with:
//   go run ./examples/02-custom-tool/
//
// No API key required — the tool is exercised locally.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/anrror/y-ai-agent-base/pkg/agent"
	"github.com/anrror/y-ai-agent-base/pkg/pipeline"
	"github.com/anrror/y-ai-agent-base/pkg/provider"
	"github.com/anrror/y-ai-agent-base/pkg/provider/openai"
	"github.com/anrror/y-ai-agent-base/pkg/tool"
	"github.com/anrror/y-ai-agent-base/pkg/types"
)

func main() {
	// #1 Create the Calculator tool with tool.FromFunction.
	//    We supply a name, description, the handler function, and a JSON schema
	//    built with the ParamSchema builder.
	calcTool := tool.FromFunction(
		"calculator",
		"Performs arithmetic operations: add or multiply two numbers.",

		// The handler receives raw JSON arguments and returns a JSON string.
		func(ctx context.Context, args json.RawMessage) (string, error) {
			var params struct {
				Operation string  `json:"operation"`
				A         float64 `json:"a"`
				B         float64 `json:"b"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}

			var result float64
			switch params.Operation {
			case "add":
				result = params.A + params.B
			case "multiply":
				result = params.A * params.B
			default:
				return "", fmt.Errorf("unknown operation %q (use 'add' or 'multiply')", params.Operation)
			}

			out, _ := json.Marshal(map[string]any{
				"operation": params.Operation,
				"a":         params.A,
				"b":         params.B,
				"result":    result,
			})
			return string(out), nil
		},

		// #2 Declare the parameter schema the LLM sees.
		tool.NewParamSchema().
			AddString("operation", "Operation to perform: 'add' or 'multiply'", true).
			AddNumber("a", "First operand", true).
			AddNumber("b", "Second operand", true).
			Build(),
	)

	// #3 Show the tool metadata.
	fmt.Printf("Tool:   %s\n", calcTool.Name())
	fmt.Printf("Desc:   %s\n", calcTool.Description())
	fmt.Printf("Schema: %s\n\n", string(calcTool.Schema()))

	// #4 Call the tool directly — no LLM involved.
	fmt.Println("=== Direct tool calls ===")

	addResult, _ := calcTool.Execute(context.Background(),
		json.RawMessage(`{"operation":"add","a":10,"b":32}`))
	fmt.Printf("add(10, 32)   => %s\n", addResult)

	mulResult, _ := calcTool.Execute(context.Background(),
		json.RawMessage(`{"operation":"multiply","a":7,"b":6}`))
	fmt.Printf("multiply(7,6) => %s\n\n", mulResult)

	// #5 Register the tool on an agent so an LLM could invoke it.
	provider := openai.NewOpenAIProvider(&provider.ProviderConfig{
		Type:    "openai",
		APIKey:  os.Getenv("OPENAI_API_KEY"),
		BaseURL: "https://api.openai.com/v1",
		Model:   "gpt-4o-mini",
	})
	p := pipeline.New(provider)

	cfg := agent.Config{
		AgentID: "calculator-agent",
		LLMConfig: types.ModelConfig{
			Model: "gpt-4o-mini",
		},
	}

	a, err := cfg.ToBuilder().
		WithProvider(provider).
		WithPipeline(p).
		WithTools(calcTool). // ← calculator tool attached here
		Build()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: build failed: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = a.Close() }()

	fmt.Printf("Agent %q has %d tool(s) attached:\n", a.ID(), len(a.Tools))
	for _, t := range a.Tools {
		fmt.Printf("  - %s: %s\n", t.Name(), t.Description())
	}
}
