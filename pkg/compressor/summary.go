package compressor

import "context"

// SummaryCompressor uses an LLM callback to generate a concise summary of
// older conversation turns while preserving the most recent messages.
type SummaryCompressor struct {
	// SummarizeFn is the callback that produces a summary. When nil the
	// compressor falls back to simple truncation.
	SummarizeFn func(ctx context.Context, text string, lang string) (string, error)
}

var _ Compressor = (*SummaryCompressor)(nil)

func (s *SummaryCompressor) Strategy() Strategy { return StrategySummary }

func (s *SummaryCompressor) Compress(ctx context.Context, messages []Message, cfg Config) ([]Message, error) {
	if s.SummarizeFn == nil || len(messages) <= 1 {
		// Fallback: keep last message only.
		if len(messages) == 0 {
			return nil, nil
		}
		return messages[len(messages)-1:], nil
	}

	// Keep the last user message, summarise everything before it.
	lastIdx := len(messages) - 1
	summary, err := s.summarizeBlock(ctx, messages[:lastIdx], cfg.SummaryLang)
	if err != nil {
		return nil, err
	}

	out := make([]Message, 0, 2)
	if summary != "" {
		out = append(out, Message{Role: "system", Content: summary, Name: "summary"})
	}
	out = append(out, messages[lastIdx])
	return out, nil
}

func (s *SummaryCompressor) summarizeBlock(ctx context.Context, msgs []Message, lang string) (string, error) {
	var text string
	for _, m := range msgs {
		text += m.Role + ": " + m.Content + "\n"
	}
	return s.SummarizeFn(ctx, text, lang)
}
