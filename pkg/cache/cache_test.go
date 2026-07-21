package cache

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestKey_Fields(t *testing.T) {
	key := Key{
		Model:       "gpt-4o",
		Messages:    `[{"role":"user","content":"hello"}]`,
		Temperature: 0.7,
		Mode:        ModeExact,
	}
	assert.Equal(t, "gpt-4o", key.Model)
	assert.Equal(t, "exact", string(key.Mode))
	assert.Equal(t, float64(0.7), key.Temperature)
}

func TestKey_ModeSemantic(t *testing.T) {
	key := Key{
		Model: "gpt-4o",
		Mode:  ModeSemantic,
	}
	assert.Equal(t, "semantic", string(key.Mode))
}

func TestEntry_Fields(t *testing.T) {
	now := time.Now()
	exp := now.Add(1 * time.Hour)
	entry := Entry{
		Content:   "test response",
		CreatedAt: now,
		ExpiresAt: exp,
		HitCount:  42,
		Embedding: []float32{0.1, 0.2, 0.3},
	}
	assert.Equal(t, "test response", entry.Content)
	assert.Equal(t, int64(42), entry.HitCount)
	assert.Len(t, entry.Embedding, 3)
	assert.True(t, entry.ExpiresAt.After(entry.CreatedAt))
}

func TestEntry_ZeroHitCount(t *testing.T) {
	entry := Entry{}
	assert.Equal(t, int64(0), entry.HitCount)
}

func TestConfig_Defaults(t *testing.T) {
	cfg := Config{}
	assert.Equal(t, Mode(""), cfg.Mode)
	assert.Equal(t, time.Duration(0), cfg.TTL)
	assert.Equal(t, 0, cfg.MaxSize)
	assert.Equal(t, float64(0), cfg.Threshold)
}

func TestConfig_ExactMode(t *testing.T) {
	cfg := Config{
		Mode:      ModeExact,
		TTL:       5 * time.Minute,
		MaxSize:   1000,
		Threshold: 0,
	}
	assert.Equal(t, ModeExact, cfg.Mode)
	assert.Equal(t, 5*time.Minute, cfg.TTL)
	assert.Equal(t, 1000, cfg.MaxSize)
}

func TestConfig_SemanticMode(t *testing.T) {
	cfg := Config{
		Mode:      ModeSemantic,
		TTL:       time.Hour,
		MaxSize:   500,
		Threshold: 0.85,
	}
	assert.Equal(t, ModeSemantic, cfg.Mode)
	assert.Equal(t, 0.85, cfg.Threshold)
}

func TestMode_Constants(t *testing.T) {
	assert.Equal(t, Mode("exact"), ModeExact)
	assert.Equal(t, Mode("semantic"), ModeSemantic)
	assert.NotEqual(t, ModeExact, ModeSemantic)
}
