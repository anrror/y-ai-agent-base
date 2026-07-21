package pipeline

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anrror/y-ai-agent-base/pkg/provider"
	"github.com/anrror/y-ai-agent-base/pkg/types"
)

const (
	postHookTimeout = 5 * time.Second
	pingTimeout     = 2 * time.Second
)

// chain is the concrete implementation of the Pipeline interface.
type chain struct {
	provider  provider.LLMProvider
	mws       []Middleware
	postHooks []PostHook
	wg        sync.WaitGroup
	shutdown  atomic.Bool // set to true when ShutdownPostHooks is called
}

// New creates a Pipeline backed by the given LLM provider, with optional
// initial Middleware.
func New(core provider.LLMProvider, mws ...Middleware) Pipeline {
	return &chain{
		provider:  core,
		mws:       mws,
		postHooks: nil,
	}
}

// Use appends one or more Middleware to the pipeline. Satisfies the Pipeline
// interface.
func (c *chain) Use(mw ...Middleware) {
	c.mws = append(c.mws, mw...)
}

// With appends a single Middleware and returns the pipeline for chaining.
func (c *chain) With(mw Middleware) *chain {
	c.mws = append(c.mws, mw)
	return c
}

// OnPostProcess registers a hook that fires asynchronously after a pipeline
// run completes (including after a stream is fully consumed). Returns the
// pipeline for chaining.
func (c *chain) OnPostProcess(hook PostHook) *chain {
	c.postHooks = append(c.postHooks, hook)
	return c
}

// Run executes the middleware chain synchronously and fills output.
// Post-hooks fire asynchronously after the response is returned to the caller.
func (c *chain) Run(ctx context.Context, input types.ChatInput) (types.ChatOutput, error) {
	if err := c.pingProvider(ctx); err != nil {
		return types.ChatOutput{}, fmt.Errorf("pipeline: %w", err)
	}

	handler := c.terminalHandler()
	for i := len(c.mws) - 1; i >= 0; i-- {
		handler = c.mws[i](handler)
	}

	var output types.ChatOutput
	err := handler(ctx, &input, &output)

	c.firePostHooks(ctx, &input, &output, err)

	return output, err
}

// ChatStream executes the middleware chain and returns a streaming response.
// Middleware pre-processing runs synchronously. The provider's streaming
// goroutine is spawned by the terminal handler, and the response is returned
// immediately with a stream channel. Post-hooks fire after the stream is fully
// consumed and the underlying provider channel closes.
func (c *chain) ChatStream(ctx context.Context, input types.ChatInput) (types.ChatOutput, error) {
	if err := c.pingProvider(ctx); err != nil {
		return types.ChatOutput{}, fmt.Errorf("pipeline: %w", err)
	}

	handler := c.streamTerminalHandler()
	for i := len(c.mws) - 1; i >= 0; i-- {
		handler = c.mws[i](handler)
	}

	var output types.ChatOutput
	err := handler(ctx, &input, &output)
	if err != nil {
		c.firePostHooks(ctx, &input, &output, err)
		return types.ChatOutput{}, err
	}

	if !output.IsStream || output.Stream == nil {
		c.firePostHooks(ctx, &input, &output, nil)
		return output, nil
	}

	// Wrap the stream channel to detect completion and fire post-hooks.
	// Use context.WithoutCancel so that Timeout middleware's deferred cancel() won't
	// kill the forwarding goroutine before the stream is fully consumed.
	streamCtx := context.WithoutCancel(ctx)
	wrapped := make(chan types.StreamEvent)
	original := output.Stream

	go func() {
		defer close(wrapped)
		var streamErr error
		for evt := range original {
			if evt.Error != nil && streamErr == nil {
				streamErr = evt.Error
			}
			select {
			case wrapped <- evt:
			case <-streamCtx.Done():
				return
			}
		}
		c.firePostHooks(streamCtx, &input, &output, streamErr)
	}()

	output.Stream = wrapped
	return output, nil
}

// terminalHandler returns the final Handler that calls provider.Chat and
// fills output with the response text.
func (c *chain) terminalHandler() Handler {
	return func(ctx context.Context, input *types.ChatInput, output *types.ChatOutput) error {
		config := types.ModelConfig{}
		if input.ModelConfig != nil {
			config = *input.ModelConfig
		}
		content, err := c.provider.Chat(ctx, input.Messages, config)
		if err != nil {
			return fmt.Errorf("pipeline chat: %w", err)
		}
		output.Content = content
		output.Role = "assistant"
		return nil
	}
}

// streamTerminalHandler returns the final Handler that calls provider.ChatStream
// and fills output with a populated Stream channel.
func (c *chain) streamTerminalHandler() Handler {
	return func(ctx context.Context, input *types.ChatInput, output *types.ChatOutput) error {
		config := types.ModelConfig{}
		if input.ModelConfig != nil {
			config = *input.ModelConfig
		}
		streamCh, err := c.provider.ChatStream(ctx, input.Messages, config)
		if err != nil {
			return fmt.Errorf("pipeline chat stream: %w", err)
		}
		output.IsStream = true
		output.Stream = streamCh
		return nil
	}
}

// firePostHooks executes all registered post-hooks concurrently. Each hook
// receives a derived context with a deadline to prevent unbounded execution.
// If the pipeline is shutting down, new hooks are skipped to avoid a race
// between wg.Add and ShutdownPostHooks/wg.Wait.
func (c *chain) firePostHooks(ctx context.Context, input *types.ChatInput, output *types.ChatOutput, runErr error) {
	if len(c.postHooks) == 0 || c.shutdown.Load() {
		return
	}

	for _, hook := range c.postHooks {
		c.wg.Add(1)
		go func(h PostHook) {
			defer c.wg.Done()
			hookCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), postHookTimeout)
			defer cancel()
			h(hookCtx, input, output, runErr)
		}(hook)
	}
}

// ShutdownPostHooks waits for all in-flight post-hooks to complete (up to
// the post-hook timeout per hook). Call during server shutdown to prevent
// goroutine leaks. After this returns, any subsequent firePostHooks calls
// are no-ops.
func (c *chain) ShutdownPostHooks() {
	c.shutdown.Store(true)
	c.wg.Wait()
}

// pingProvider sends a short-lived health check to the provider before
// running the middleware chain, so that unreachable providers fail fast.
func (c *chain) pingProvider(ctx context.Context) error {
	pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	if err := c.provider.Ping(pingCtx); err != nil {
		return fmt.Errorf("provider ping: %w", err)
	}
	return nil
}
