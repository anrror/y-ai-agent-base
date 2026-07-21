// Package compressor provides pluggable context compression strategies.
// Built-in implementations: SummaryCompressor and TruncateCompressor.
package compressor

import "context"

// Strategy identifies a compression algorithm.
type Strategy string

const (
	StrategySummary  Strategy = "summary"   // LLM-driven summarization
	StrategyTruncate Strategy = "truncate"  // token-count-based truncation
	StrategySemantic Strategy = "semantic"  // relevance-based retention
	StrategyHybrid   Strategy = "hybrid"    // summary + truncate combination
)

// Config controls compression behaviour.
type Config struct {
	MaxTokens     int    // target token count after compression (0 = no limit)
	SummaryLang   string // target language for summaries (e.g. "zh", "en")
	PreserveRoles bool   // keep system messages and most recent user message
}

// Compressor reduces the token footprint of a message sequence while
// preserving semantic integrity.
type Compressor interface {
	// Compress returns a reduced copy of messages. The original slice
	// must not be modified.
	Compress(ctx context.Context, messages []Message, cfg Config) ([]Message, error)

	// Strategy identifies the compression approach.
	Strategy() Strategy
}

// Message is a lightweight type decoupled from types.Message.
type Message struct {
	Role    string
	Content string
	Name    string
}
