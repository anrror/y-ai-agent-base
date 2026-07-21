package agent

import (
	"context"
	"fmt"

	"github.com/anrror/y-ai-agent-base/pkg/pipeline"
	"github.com/anrror/y-ai-agent-base/pkg/types"
)

// pipelineWrapper wraps a Pipeline with per-agent extension middlewares.
// Extension middlewares run outside (before/after) the inner pipeline's own
// middleware chain. This prevents cross-contamination when multiple agents
// share the same underlying pipeline: each agent gets its own wrapper with
// its own extension middlewares, and the shared pipeline is never mutated
// via Use().
type pipelineWrapper struct {
	inner pipeline.Pipeline
	mws   []pipeline.Middleware
}

// Use appends middlewares to this wrapper's chain (NOT the inner pipeline).
func (pw *pipelineWrapper) Use(mw ...pipeline.Middleware) {
	pw.mws = append(pw.mws, mw...)
}

// Run chains extension middlewares around the inner pipeline's Run.
func (pw *pipelineWrapper) Run(ctx context.Context, input types.ChatInput) (types.ChatOutput, error) {
	handler := pipeline.Handler(func(ctx context.Context, input *types.ChatInput, output *types.ChatOutput) error {
		out, err := pw.inner.Run(ctx, *input)
		if err != nil {
			return fmt.Errorf("pipeline wrapper: %w", err)
		}
		*output = out
		return nil
	})

	for i := len(pw.mws) - 1; i >= 0; i-- {
		handler = pw.mws[i](handler)
	}

	var output types.ChatOutput
	err := handler(ctx, &input, &output)
	return output, err
}

// ChatStream chains extension middlewares around the inner pipeline's ChatStream.
func (pw *pipelineWrapper) ChatStream(ctx context.Context, input types.ChatInput) (types.ChatOutput, error) {
	handler := pipeline.Handler(func(ctx context.Context, input *types.ChatInput, output *types.ChatOutput) error {
		out, err := pw.inner.ChatStream(ctx, *input)
		if err != nil {
			return fmt.Errorf("pipeline wrapper: %w", err)
		}
		*output = out
		return nil
	})

	for i := len(pw.mws) - 1; i >= 0; i-- {
		handler = pw.mws[i](handler)
	}

	var output types.ChatOutput
	err := handler(ctx, &input, &output)
	return output, err
}
