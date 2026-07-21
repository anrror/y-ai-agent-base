package types

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMessage_JSONRoundTrip(t *testing.T) {
	original := Message{Role: "user", Content: "hello world"}

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded Message
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, original, decoded)
}

func TestModelConfig_JSONRoundTrip(t *testing.T) {
	seed := 42
	original := ModelConfig{
		Provider:    "openai",
		Model:       "gpt-4",
		Temperature: 0.7,
		MaxTokens:   4096,
		TopP:        0.95,
		Seed:        &seed,
	}

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded ModelConfig
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, original, decoded)
	assert.Equal(t, original.Seed, decoded.Seed)
}

func TestModelConfig_JSONRoundTrip_WithoutSeed(t *testing.T) {
	original := ModelConfig{
		Provider:    "anthropic",
		Model:       "claude-3",
		Temperature: 0.5,
	}

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded ModelConfig
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, original, decoded)
	assert.Nil(t, decoded.Seed)
}

func TestChatInput_JSONRoundTrip(t *testing.T) {
	original := ChatInput{
		Messages: []Message{
			{Role: "system", Content: "You are a helpful assistant."},
			{Role: "user", Content: "What is Go?"},
		},
		ModelConfig: &ModelConfig{
			Provider:    "openai",
			Model:       "gpt-4",
			Temperature: 0.7,
		},
		SafetyConfig: &SafetyConfig{
			Enabled:        true,
			BlockThreshold: 0.9,
			Categories:     []string{"hate", "violence"},
		},
		Tools:    []string{"search", "calculator"},
		Metadata: map[string]any{"source": "cli"},
	}

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded ChatInput
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, original.Messages, decoded.Messages)
	assert.Equal(t, original.ModelConfig, decoded.ModelConfig)
	assert.Equal(t, original.SafetyConfig, decoded.SafetyConfig)
	assert.Equal(t, original.Tools, decoded.Tools)
	assert.Equal(t, original.Metadata, decoded.Metadata)
}

func TestChatOutput_JSONRoundTrip(t *testing.T) {
	original := ChatOutput{
		Content:      "Go is a statically typed, compiled language.",
		Role:         "assistant",
		FinishReason: "stop",
		IsStream:     false,
		Model:        "gpt-4",
		Usage: &UsageInfo{
			PromptTokens:     20,
			CompletionTokens: 15,
			TotalTokens:      35,
		},
	}

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded ChatOutput
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, original.Content, decoded.Content)
	assert.Equal(t, original.Role, decoded.Role)
	assert.Equal(t, original.FinishReason, decoded.FinishReason)
	assert.False(t, decoded.IsStream, "IsStream should default to false")
	assert.Equal(t, original.Usage, decoded.Usage)
}

func TestChatOutput_IsStreamDefaultsFalse(t *testing.T) {
	// Zero-value ChatOutput should have IsStream == false.
	var output ChatOutput
	assert.False(t, output.IsStream, "zero-value ChatOutput.IsStream should be false")

	// JSON without is_stream should decode with IsStream == false.
	jsonInput := `{"content":"hello","role":"assistant"}`
	var decoded ChatOutput
	err := json.Unmarshal([]byte(jsonInput), &decoded)
	require.NoError(t, err)
	assert.False(t, decoded.IsStream, "IsStream should be false when omitted from JSON")
}

func TestSafetyConfig_JSONRoundTrip(t *testing.T) {
	original := SafetyConfig{
		Enabled:        true,
		BlockThreshold: 0.85,
		WarnThreshold:  0.6,
		Categories:     []string{"self_harm", "illegal"},
	}

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded SafetyConfig
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, original, decoded)
}

func TestMemoryEntry_JSONRoundTrip(t *testing.T) {
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	original := MemoryEntry{
		ID:         "mem-001",
		Content:    "User prefers short answers.",
		Importance: 0.8,
		CreatedAt:  now,
		AccessedAt: now.Add(time.Hour),
		Metadata:   map[string]any{"source": "chat"},
	}

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded MemoryEntry
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, original.ID, decoded.ID)
	assert.Equal(t, original.Content, decoded.Content)
	assert.Equal(t, original.Importance, decoded.Importance)
	assert.WithinDuration(t, original.CreatedAt, decoded.CreatedAt, time.Second)
	assert.WithinDuration(t, original.AccessedAt, decoded.AccessedAt, time.Second)
	assert.Equal(t, original.Metadata, decoded.Metadata)
}
