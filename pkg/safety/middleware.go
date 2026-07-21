package safety

import (
	"context"

	"github.com/anrror/y-ai-agent-base/pkg/pipeline"
	"github.com/anrror/y-ai-agent-base/pkg/types"
)

// SafetyMiddleware returns a pipeline.Middleware that applies input and output
// safety checks through the Guard. Input is checked before the pipeline runs;
// output is checked after a successful pipeline execution. When a check fails
// the middleware returns ErrGuardBlocked without proceeding.
func SafetyMiddleware(guard *Guard) pipeline.Middleware {
	return func(next pipeline.Handler) pipeline.Handler {
		return func(ctx context.Context, input *types.ChatInput, output *types.ChatOutput) error {
			// Input guard: check the last user message before pipeline execution.
			if err := guard.CheckInput(ctx, lastUserMessage(input.Messages)); err != nil {
				return err
			}

			// Execute the downstream pipeline.
			if err := next(ctx, input, output); err != nil {
				return err
			}

			// Output guard: check the generated content after a successful run.
			if err := guard.CheckOutput(ctx, output.Content); err != nil {
				return err
			}

			return nil
		}
	}
}

// lastUserMessage returns the content of the most recent user message.
// Returns an empty string when no user message is present.
func lastUserMessage(messages []types.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			return messages[i].Content
		}
	}
	return ""
}
