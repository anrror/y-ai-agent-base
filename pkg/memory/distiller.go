package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anrror/y-ai-agent-base/pkg/provider"
	"github.com/anrror/y-ai-agent-base/pkg/types"
)

// MemoryExtract is the distilled output of a conversation turn.
type MemoryExtract struct {
	// Summary is a concise one-paragraph summary of the key information.
	Summary string `json:"summary"`

	// KeyPoints is a list of discrete facts or takeaways.
	KeyPoints []string `json:"key_points"`

	// Importance is a score from 0.0 (trivial) to 1.0 (critical).
	Importance float64 `json:"importance"`
}

// Distiller uses an LLM to summarize conversation turns into MemoryExtract
// records suitable for long-term storage.
type Distiller struct {
	llm provider.LLMProvider
}

// NewDistiller returns a Distiller backed by the given LLM provider. Pass nil
// to disable distillation; Distill will return a fallback extract.
func NewDistiller(llm provider.LLMProvider) *Distiller {
	return &Distiller{llm: llm}
}

const distillerSystemPrompt = `You are a memory distiller. Your job is to extract the essential information from a conversation turn.

Given a USER message and an ASSISTANT reply, produce a JSON object with:
- "summary": A concise one-paragraph summary (max 3 sentences).
- "key_points": An array of up to 5 discrete facts or takeaways as short strings.
- "importance": A float from 0.0 (trivial) to 1.0 (critical) indicating how important this turn is to remember long-term.

Respond with ONLY the JSON object, no other text.`

// Distill extracts a MemoryExtract from the given user message and assistant
// reply. When no LLM is configured a fallback extract with the raw content and
// zero importance is returned.
func (d *Distiller) Distill(ctx context.Context, message, reply string) (*MemoryExtract, error) {
	if d.llm == nil {
		return d.fallbackExtract(message, reply), nil
	}

	messages := []types.Message{
		{Role: "system", Content: distillerSystemPrompt},
		{Role: "user", Content: fmt.Sprintf("USER: %s\nASSISTANT: %s", message, reply)},
	}

	resp, err := d.llm.Chat(ctx, messages, types.ModelConfig{Temperature: 0.3})
	if err != nil {
		return nil, fmt.Errorf("distillation failed: %w", err)
	}

	extract, err := parseExtract(resp)
	if err != nil {
		return nil, fmt.Errorf("failed to parse distill response: %w", err)
	}
	return extract, nil
}

// fallbackExtract returns a basic extract when no LLM is available.
func (d *Distiller) fallbackExtract(message, reply string) *MemoryExtract {
	summary := truncate(message, 200) + " → " + truncate(reply, 200)
	return &MemoryExtract{
		Summary:    summary,
		KeyPoints:  []string{truncate(message, 100)},
		Importance: 0.0,
	}
}

func parseExtract(raw string) (*MemoryExtract, error) {
	raw = strings.TrimSpace(raw)

	// Strip markdown code fences if present.
	if strings.HasPrefix(raw, "```") {
		raw = strings.TrimPrefix(raw, "```json")
		raw = strings.TrimPrefix(raw, "```")
		raw = strings.TrimSuffix(raw, "```")
		raw = strings.TrimSpace(raw)
	}

	extract := &MemoryExtract{}
	if err := json.Unmarshal([]byte(raw), extract); err != nil {
		return nil, fmt.Errorf("invalid JSON extract: %w", err)
	}
	if extract.Summary == "" {
		return nil, fmt.Errorf("distilled summary is empty")
	}
	return extract, nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
