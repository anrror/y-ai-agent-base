// Package safety defines content safety guard types.
package safety

import (
	"context"
	"fmt"

	"github.com/anrror/y-ai-agent-base/pkg/provider"
	"github.com/anrror/y-ai-agent-base/pkg/types"
)

// Guard wraps a GuardProvider and applies safety configuration.
type Guard struct {
	Provider provider.GuardProvider
	Config   types.SafetyConfig
}

// Check runs a safety check against the given input text.
// It returns (true, nil) when the content is allowed. When the Guard
// has no Provider configured, all content passes through.
func (g *Guard) Check(ctx context.Context, text string) (bool, error) {
	if g.Provider == nil {
		return true, nil
	}
	if !g.Config.Enabled {
		return true, nil
	}
	allowed, err := g.Provider.Check(ctx, text)
	if err != nil {
		return false, fmt.Errorf("guard check: %w", err)
	}
	return allowed, nil
}

// CheckInput runs an input safety check. It respects both the global Enabled
// toggle and the InputGuard toggle. When the Guard has no Provider configured
// the check is skipped. Returns nil when the text is allowed,
// or ErrGuardBlocked when it is not.
func (g *Guard) CheckInput(ctx context.Context, text string) error {
	if g.Provider == nil || !g.Config.Enabled || !g.Config.InputGuard {
		return nil
	}
	allowed, err := g.Provider.Check(ctx, text)
	if err != nil {
		return fmt.Errorf("guard check input: %w", err)
	}
	if !allowed {
		return types.ErrGuardBlocked
	}
	return nil
}

// CheckOutput runs an output safety check. It respects both the global Enabled
// toggle and the OutputGuard toggle. When the Guard has no Provider configured
// the check is skipped. Returns nil when the text is allowed,
// or ErrGuardBlocked when it is not.
func (g *Guard) CheckOutput(ctx context.Context, text string) error {
	if g.Provider == nil || !g.Config.Enabled || !g.Config.OutputGuard {
		return nil
	}
	allowed, err := g.Provider.Check(ctx, text)
	if err != nil {
		return fmt.Errorf("guard check output: %w", err)
	}
	if !allowed {
		return types.ErrGuardBlocked
	}
	return nil
}

// SafetyNoticeEnabled reports whether safety notices should be shown to users.
// When no Provider is configured it returns the raw config toggle.
func (g *Guard) SafetyNoticeEnabled() bool {
	if g.Provider == nil {
		return g.Config.Enabled
	}
	return g.Config.Enabled
}
