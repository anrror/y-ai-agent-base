package safety_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anrror/y-ai-agent-base/pkg/pipeline"
	"github.com/anrror/y-ai-agent-base/pkg/safety"
	"github.com/anrror/y-ai-agent-base/pkg/types"
)

// ---------------------------------------------------------------------------
// MockGuard
// ---------------------------------------------------------------------------

func TestMockGuard_SafeText_returnsAllowed(t *testing.T) {
	mg := &safety.MockGuard{}
	allowed, err := mg.Check(context.Background(), "hello world")
	assert.True(t, allowed)
	assert.NoError(t, err)
}

func TestMockGuard_BadText_returnsBlocked(t *testing.T) {
	mg := &safety.MockGuard{}
	allowed, err := mg.Check(context.Background(), "this is bad content")
	assert.False(t, allowed)
	assert.NoError(t, err)
}

func TestMockGuard_AllowAll_returnsAllowedForBadText(t *testing.T) {
	mg := &safety.MockGuard{AllowAll: true}
	allowed, err := mg.Check(context.Background(), "this is bad content")
	assert.True(t, allowed)
	assert.NoError(t, err)
}

func TestMockGuard_BlockAll_returnsBlockedForSafeText(t *testing.T) {
	mg := &safety.MockGuard{BlockAll: true}
	allowed, err := mg.Check(context.Background(), "hello world")
	assert.False(t, allowed)
	assert.NoError(t, err)
}

// ---------------------------------------------------------------------------
// Guard.Check — base method
// ---------------------------------------------------------------------------

func TestGuard_Check_Enabled_true_allowed(t *testing.T) {
	guard := &safety.Guard{
		Provider: &safety.MockGuard{},
		Config:   types.SafetyConfig{Enabled: true},
	}
	allowed, err := guard.Check(context.Background(), "hello world")
	assert.True(t, allowed)
	assert.NoError(t, err)
}

func TestGuard_Check_Enabled_true_blocked(t *testing.T) {
	guard := &safety.Guard{
		Provider: &safety.MockGuard{},
		Config:   types.SafetyConfig{Enabled: true},
	}
	allowed, err := guard.Check(context.Background(), "this is bad content")
	assert.False(t, allowed)
	assert.NoError(t, err)
}

func TestGuard_Check_Enabled_false_skipsGuard(t *testing.T) {
	guard := &safety.Guard{
		Provider: &safety.MockGuard{},
		Config:   types.SafetyConfig{Enabled: false},
	}
	// Even "bad" text passes when the guard is disabled.
	allowed, err := guard.Check(context.Background(), "this is bad content")
	assert.True(t, allowed)
	assert.NoError(t, err)
}

// ---------------------------------------------------------------------------
// Guard.CheckInput
// ---------------------------------------------------------------------------

func TestGuard_CheckInput_enabled_and_inputGuard_true_safeText(t *testing.T) {
	guard := &safety.Guard{
		Provider: &safety.MockGuard{},
		Config:   types.SafetyConfig{Enabled: true, InputGuard: true},
	}
	err := guard.CheckInput(context.Background(), "hello world")
	assert.NoError(t, err)
}

func TestGuard_CheckInput_enabled_and_inputGuard_true_badText(t *testing.T) {
	guard := &safety.Guard{
		Provider: &safety.MockGuard{},
		Config:   types.SafetyConfig{Enabled: true, InputGuard: true},
	}
	err := guard.CheckInput(context.Background(), "this is bad content")
	assert.ErrorIs(t, err, types.ErrGuardBlocked)
}

func TestGuard_CheckInput_enabled_inputGuard_false_skipsGuard(t *testing.T) {
	guard := &safety.Guard{
		Provider: &safety.MockGuard{},
		Config:   types.SafetyConfig{Enabled: true, InputGuard: false},
	}
	err := guard.CheckInput(context.Background(), "this is bad content")
	assert.NoError(t, err)
}

func TestGuard_CheckInput_disabled_inputGuard_true_skipsGuard(t *testing.T) {
	guard := &safety.Guard{
		Provider: &safety.MockGuard{},
		Config:   types.SafetyConfig{Enabled: false, InputGuard: true},
	}
	err := guard.CheckInput(context.Background(), "this is bad content")
	assert.NoError(t, err)
}

func TestGuard_CheckInput_disabled_inputGuard_false_skipsGuard(t *testing.T) {
	guard := &safety.Guard{
		Provider: &safety.MockGuard{},
		Config:   types.SafetyConfig{Enabled: false, InputGuard: false},
	}
	err := guard.CheckInput(context.Background(), "this is bad content")
	assert.NoError(t, err)
}

// ---------------------------------------------------------------------------
// Guard.CheckOutput
// ---------------------------------------------------------------------------

func TestGuard_CheckOutput_enabled_and_outputGuard_true_safeText(t *testing.T) {
	guard := &safety.Guard{
		Provider: &safety.MockGuard{},
		Config:   types.SafetyConfig{Enabled: true, OutputGuard: true},
	}
	err := guard.CheckOutput(context.Background(), "hello world")
	assert.NoError(t, err)
}

func TestGuard_CheckOutput_enabled_and_outputGuard_true_badText(t *testing.T) {
	guard := &safety.Guard{
		Provider: &safety.MockGuard{},
		Config:   types.SafetyConfig{Enabled: true, OutputGuard: true},
	}
	err := guard.CheckOutput(context.Background(), "this is bad content")
	assert.ErrorIs(t, err, types.ErrGuardBlocked)
}

func TestGuard_CheckOutput_enabled_outputGuard_false_skipsGuard(t *testing.T) {
	guard := &safety.Guard{
		Provider: &safety.MockGuard{},
		Config:   types.SafetyConfig{Enabled: true, OutputGuard: false},
	}
	err := guard.CheckOutput(context.Background(), "this is bad content")
	assert.NoError(t, err)
}

func TestGuard_CheckOutput_disabled_outputGuard_true_skipsGuard(t *testing.T) {
	guard := &safety.Guard{
		Provider: &safety.MockGuard{},
		Config:   types.SafetyConfig{Enabled: false, OutputGuard: true},
	}
	err := guard.CheckOutput(context.Background(), "this is bad content")
	assert.NoError(t, err)
}

func TestGuard_CheckOutput_disabled_outputGuard_false_skipsGuard(t *testing.T) {
	guard := &safety.Guard{
		Provider: &safety.MockGuard{},
		Config:   types.SafetyConfig{Enabled: false, OutputGuard: false},
	}
	err := guard.CheckOutput(context.Background(), "this is bad content")
	assert.NoError(t, err)
}

// ---------------------------------------------------------------------------
// SafetyMiddleware — input guard
// ---------------------------------------------------------------------------

func TestSafetyMiddleware_safeInput_runsPipeline(t *testing.T) {
	guard := &safety.Guard{
		Provider: &safety.MockGuard{},
		Config:   types.SafetyConfig{Enabled: true, InputGuard: true, OutputGuard: false},
	}
	mw := safety.SafetyMiddleware(guard)

	called := false
	next := pipeline.Handler(func(ctx context.Context, input *types.ChatInput, output *types.ChatOutput) error {
		called = true
		output.Content = "assistant response"; return nil
	})

	handler := mw(next)
	var output types.ChatOutput
	err := handler(context.Background(), &types.ChatInput{
		Messages: []types.Message{{Role: "user", Content: "hello world"}},
	}, &output)
	require.NoError(t, err)
	assert.True(t, called)
	assert.Equal(t, "assistant response", output.Content)
}

func TestSafetyMiddleware_badInput_blocksBeforePipeline(t *testing.T) {
	guard := &safety.Guard{
		Provider: &safety.MockGuard{},
		Config:   types.SafetyConfig{Enabled: true, InputGuard: true, OutputGuard: false},
	}
	mw := safety.SafetyMiddleware(guard)

	called := false
	next := pipeline.Handler(func(ctx context.Context, input *types.ChatInput, output *types.ChatOutput) error {
		called = true
		output.Content = "should not reach"; return nil
	})

	handler := mw(next)
	err := handler(context.Background(), &types.ChatInput{
		Messages: []types.Message{{Role: "user", Content: "this is bad"}},
	}, &types.ChatOutput{})
	assert.ErrorIs(t, err, types.ErrGuardBlocked)
	assert.False(t, called, "next handler should not be called when input is blocked")
}

func TestSafetyMiddleware_inputGuard_disabled_badText_passes(t *testing.T) {
	guard := &safety.Guard{
		Provider: &safety.MockGuard{},
		Config:   types.SafetyConfig{Enabled: true, InputGuard: false, OutputGuard: false},
	}
	mw := safety.SafetyMiddleware(guard)

	called := false
	next := pipeline.Handler(func(ctx context.Context, input *types.ChatInput, output *types.ChatOutput) error {
		called = true
		output.Content = "ok"; return nil
	})

	handler := mw(next)
	err := handler(context.Background(), &types.ChatInput{
		Messages: []types.Message{{Role: "user", Content: "this is bad"}},
	}, &types.ChatOutput{})
	assert.NoError(t, err)
	assert.True(t, called)
}

func TestSafetyMiddleware_noUserMessages_passes(t *testing.T) {
	guard := &safety.Guard{
		Provider: &safety.MockGuard{},
		Config:   types.SafetyConfig{Enabled: true, InputGuard: true, OutputGuard: false},
	}
	mw := safety.SafetyMiddleware(guard)

	called := false
	next := pipeline.Handler(func(ctx context.Context, input *types.ChatInput, output *types.ChatOutput) error {
		called = true
		output.Content = "ok"; return nil
	})

	handler := mw(next)
	err := handler(context.Background(), &types.ChatInput{
		Messages: []types.Message{{Role: "assistant", Content: "hi"}},
	}, &types.ChatOutput{})
	assert.NoError(t, err)
	assert.True(t, called)
}

// ---------------------------------------------------------------------------
// SafetyMiddleware — output guard
// ---------------------------------------------------------------------------

func TestSafetyMiddleware_safeOutput_passes(t *testing.T) {
	guard := &safety.Guard{
		Provider: &safety.MockGuard{},
		Config:   types.SafetyConfig{Enabled: true, InputGuard: false, OutputGuard: true},
	}
	mw := safety.SafetyMiddleware(guard)

	next := pipeline.Handler(func(ctx context.Context, input *types.ChatInput, output *types.ChatOutput) error {
		output.Content = "safe response"; return nil
	})

	handler := mw(next)
	var output types.ChatOutput
	err := handler(context.Background(), &types.ChatInput{}, &output)
	require.NoError(t, err)
	assert.Equal(t, "safe response", output.Content)
}

func TestSafetyMiddleware_badOutput_blocksAfterPipeline(t *testing.T) {
	guard := &safety.Guard{
		Provider: &safety.MockGuard{},
		Config:   types.SafetyConfig{Enabled: true, InputGuard: false, OutputGuard: true},
	}
	mw := safety.SafetyMiddleware(guard)

	next := pipeline.Handler(func(ctx context.Context, input *types.ChatInput, output *types.ChatOutput) error {
		output.Content = "this output is bad"; return nil
	})

	handler := mw(next)
	err := handler(context.Background(), &types.ChatInput{}, &types.ChatOutput{})
	assert.ErrorIs(t, err, types.ErrGuardBlocked)
}

func TestSafetyMiddleware_pipelineError_outputGuardSkipped(t *testing.T) {
	guard := &safety.Guard{
		Provider: &safety.MockGuard{},
		Config:   types.SafetyConfig{Enabled: true, InputGuard: false, OutputGuard: true},
	}
	mw := safety.SafetyMiddleware(guard)

	pipelineErr := errors.New("pipeline failed")
	next := pipeline.Handler(func(ctx context.Context, input *types.ChatInput, output *types.ChatOutput) error {
		output.Content = "this output is bad"; return pipelineErr
	})

	handler := mw(next)
	err := handler(context.Background(), &types.ChatInput{}, &types.ChatOutput{})
	assert.ErrorIs(t, err, pipelineErr, "pipeline error should propagate, not be overwritten by guard")
}

// ---------------------------------------------------------------------------
// SafetyMiddleware — combined input + output guard
// ---------------------------------------------------------------------------

func TestSafetyMiddleware_bothGuards_safeInputAndOutput_passes(t *testing.T) {
	guard := &safety.Guard{
		Provider: &safety.MockGuard{},
		Config:   types.SafetyConfig{Enabled: true, InputGuard: true, OutputGuard: true},
	}
	mw := safety.SafetyMiddleware(guard)

	next := pipeline.Handler(func(ctx context.Context, input *types.ChatInput, output *types.ChatOutput) error {
		output.Content = "safe response"; return nil
	})

	handler := mw(next)
	var output types.ChatOutput
	err := handler(context.Background(), &types.ChatInput{
		Messages: []types.Message{{Role: "user", Content: "hello"}},
	}, &output)
	require.NoError(t, err)
	assert.Equal(t, "safe response", output.Content)
}

func TestSafetyMiddleware_bothGuards_badInput_blocksBeforePipeline(t *testing.T) {
	guard := &safety.Guard{
		Provider: &safety.MockGuard{},
		Config:   types.SafetyConfig{Enabled: true, InputGuard: true, OutputGuard: true},
	}
	mw := safety.SafetyMiddleware(guard)

	called := false
	next := pipeline.Handler(func(ctx context.Context, input *types.ChatInput, output *types.ChatOutput) error {
		called = true
		return nil
	})

	handler := mw(next)
	err := handler(context.Background(), &types.ChatInput{
		Messages: []types.Message{{Role: "user", Content: "this is bad"}},
	}, &types.ChatOutput{})
	assert.ErrorIs(t, err, types.ErrGuardBlocked)
	assert.False(t, called)
}

func TestSafetyMiddleware_bothGuards_safeInput_badOutput_blocks(t *testing.T) {
	guard := &safety.Guard{
		Provider: &safety.MockGuard{},
		Config:   types.SafetyConfig{Enabled: true, InputGuard: true, OutputGuard: true},
	}
	mw := safety.SafetyMiddleware(guard)

	next := pipeline.Handler(func(ctx context.Context, input *types.ChatInput, output *types.ChatOutput) error {
		output.Content = "bad output"; return nil
	})

	handler := mw(next)
	err := handler(context.Background(), &types.ChatInput{
		Messages: []types.Message{{Role: "user", Content: "hello"}},
	}, &types.ChatOutput{})
	assert.ErrorIs(t, err, types.ErrGuardBlocked)
}

// ---------------------------------------------------------------------------
// SafetyMiddleware — all guard disabled
// ---------------------------------------------------------------------------

func TestSafetyMiddleware_guardDisabled_badInputAndOutput_passes(t *testing.T) {
	guard := &safety.Guard{
		Provider: &safety.MockGuard{},
		Config:   types.SafetyConfig{Enabled: false, InputGuard: false, OutputGuard: false},
	}
	mw := safety.SafetyMiddleware(guard)

	next := pipeline.Handler(func(ctx context.Context, input *types.ChatInput, output *types.ChatOutput) error {
		output.Content = "bad output"; return nil
	})

	handler := mw(next)
	err := handler(context.Background(), &types.ChatInput{
		Messages: []types.Message{{Role: "user", Content: "this is bad"}},
	}, &types.ChatOutput{})
	assert.NoError(t, err)
}

// ---------------------------------------------------------------------------
// SafetyMiddleware — last user message extraction
// ---------------------------------------------------------------------------

func TestSafetyMiddleware_extractsLastUserMessage(t *testing.T) {
	guard := &safety.Guard{
		Provider: &safety.MockGuard{},
		Config:   types.SafetyConfig{Enabled: true, InputGuard: true, OutputGuard: false},
	}
	mw := safety.SafetyMiddleware(guard)

	called := false
	next := pipeline.Handler(func(ctx context.Context, input *types.ChatInput, output *types.ChatOutput) error {
		called = true
		output.Content = "ok"; return nil
	})

	handler := mw(next)
	err := handler(context.Background(), &types.ChatInput{
		Messages: []types.Message{
			{Role: "user", Content: "first"},
			{Role: "assistant", Content: "reply"},
			{Role: "user", Content: "second"},
		},
	}, &types.ChatOutput{})
	assert.NoError(t, err)
	assert.True(t, called)
}

func TestSafetyMiddleware_lastUserMessageBad_blocks(t *testing.T) {
	guard := &safety.Guard{
		Provider: &safety.MockGuard{},
		Config:   types.SafetyConfig{Enabled: true, InputGuard: true, OutputGuard: false},
	}
	mw := safety.SafetyMiddleware(guard)

	called := false
	next := pipeline.Handler(func(ctx context.Context, input *types.ChatInput, output *types.ChatOutput) error {
		called = true
		return nil
	})

	handler := mw(next)
	err := handler(context.Background(), &types.ChatInput{
		Messages: []types.Message{
			{Role: "user", Content: "good"},
			{Role: "user", Content: "this is bad"},
		},
	}, &types.ChatOutput{})
	assert.ErrorIs(t, err, types.ErrGuardBlocked)
	assert.False(t, called, "only the last user message is 'bad', but it should still block")
}
