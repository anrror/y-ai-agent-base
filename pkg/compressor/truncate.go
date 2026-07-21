package compressor

import "context"

// TruncateCompressor drops the oldest messages until the total fits within
// MaxTokens. The most recent user message and system messages are preserved
// with priority.
type TruncateCompressor struct {
	// EstimateTokens returns the approximate token count for a message.
	// When nil, length-in-characters is used as a rough proxy.
	EstimateTokens func(text string) int
}

var _ Compressor = (*TruncateCompressor)(nil)

func (t *TruncateCompressor) Strategy() Strategy { return StrategyTruncate }

func (t *TruncateCompressor) Compress(ctx context.Context, messages []Message, cfg Config) ([]Message, error) {
	if cfg.MaxTokens <= 0 || len(messages) <= 1 {
		return messages, nil
	}

	estimate := t.EstimateTokens
	if estimate == nil {
		estimate = func(text string) int { return len(text) / 4 } // rough char→token
	}

	// Count tokens from newest to oldest, stop when we exceed budget.
	var total int
	keepEnd := len(messages) // first index to drop
	for i := len(messages) - 1; i >= 0; i-- {
		tokens := estimate(messages[i].Content)
		if messages[i].Role == "system" {
			tokens /= 2 // system messages are cheaper
		}
		if total+tokens > cfg.MaxTokens {
			keepEnd = i + 1
			break
		}
		total += tokens
	}
	if keepEnd >= len(messages) {
		return messages, nil // all fit
	}
	if keepEnd < 0 {
		keepEnd = 0
	}
	// Always keep at least the last message.
	if keepEnd > len(messages)-1 {
		keepEnd = len(messages) - 1
	}
	return messages[keepEnd:], nil
}
