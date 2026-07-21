// Package pipeline defines processing pipeline types and interfaces.
package pipeline

import (
	"context"

	"github.com/anrror/y-ai-agent-base/pkg/types"
)

// Handler processes a ChatInput into a ChatOutput.
// The input is a pointer so middleware can enrich it (e.g. inject tenant
// identity). The output is a pointer that the terminal handler fills;
// middleware may read or modify it after calling next.
type Handler func(ctx context.Context, input *types.ChatInput, output *types.ChatOutput) error

// Middleware wraps a Handler with pre/post-processing logic.
type Middleware func(next Handler) Handler

// Pipeline chains Middleware instances and terminates at a terminal Handler.
type Pipeline interface {
	Use(mw ...Middleware)
	Run(ctx context.Context, input types.ChatInput) (types.ChatOutput, error)
	ChatStream(ctx context.Context, input types.ChatInput) (types.ChatOutput, error)
}

// PostHook is invoked after a Pipeline completes, regardless of success or failure.
// Both input and output point to the values used by the handler chain.
type PostHook func(ctx context.Context, input *types.ChatInput, output *types.ChatOutput, err error)
