package builtin

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTimeSkill_Instantiation(t *testing.T) {
	sk := TimeSkill()
	assert.Equal(t, "time", sk.Name())
	assert.NotEmpty(t, sk.Description())
	assert.NotEmpty(t, sk.Instructions())
	assert.NotEmpty(t, sk.Metadata().Tags)
	assert.NotEmpty(t, sk.Tools())
	assert.Equal(t, "utility", sk.Metadata().Category)
	assert.Equal(t, "1.0.0", sk.Metadata().Version)
}

func TestTimeSkill_Match(t *testing.T) {
	sk := TimeSkill()
	ctx := context.Background()

	// Should match time-related queries.
	score := sk.Match(ctx, "what time is it")
	assert.Greater(t, score, float64(0))

	score = sk.Match(ctx, "today's date please")
	assert.Greater(t, score, float64(0))

	score = sk.Match(ctx, "now")
	assert.Greater(t, score, float64(0))

	// Should NOT match unrelated queries.
	score = sk.Match(ctx, "translate hello to french")
	assert.Equal(t, float64(0), score)
}

func TestTimeSkill_Tools(t *testing.T) {
	sk := TimeSkill()
	tools := sk.Tools()
	assert.Len(t, tools, 1)
	assert.Equal(t, "get_current_time", tools[0].Name())

	result, err := tools[0].Execute(context.Background(), nil)
	require.NoError(t, err)
	assert.NotEmpty(t, result)
}

func TestEchoSkill_Instantiation(t *testing.T) {
	sk := EchoSkill()
	assert.Equal(t, "echo", sk.Name())
	assert.NotEmpty(t, sk.Description())
	assert.NotEmpty(t, sk.Instructions())
	assert.NotEmpty(t, sk.Metadata().Tags)
	assert.Len(t, sk.Tools(), 1)
	assert.Equal(t, "utility", sk.Metadata().Category)
}

func TestEchoSkill_Match(t *testing.T) {
	sk := EchoSkill()
	ctx := context.Background()

	score := sk.Match(ctx, "repeat after me hello")
	assert.Greater(t, score, float64(0))

	score = sk.Match(ctx, "echo this back")
	assert.Greater(t, score, float64(0))

	score = sk.Match(ctx, "mirror what I say")
	assert.Greater(t, score, float64(0))

	score = sk.Match(ctx, "what is the weather")
	assert.Equal(t, float64(0), score)
}

func TestEchoSkill_Tools(t *testing.T) {
	sk := EchoSkill()
	tools := sk.Tools()
	assert.Len(t, tools, 1)
	assert.Equal(t, "echo", tools[0].Name())

	result, err := tools[0].Execute(context.Background(),
		[]byte(`{"message":"hello world"}`))
	require.NoError(t, err)
	assert.Contains(t, result, "hello world")
}

func TestWeatherSkill_Instantiation(t *testing.T) {
	sk := WeatherSkill()
	assert.Equal(t, "weather", sk.Name())
	assert.NotEmpty(t, sk.Description())
	assert.NotEmpty(t, sk.Instructions())
	assert.NotEmpty(t, sk.Metadata().Tags)
	assert.Len(t, sk.Tools(), 1)
	assert.Equal(t, "utility", sk.Metadata().Category)
	assert.Equal(t, "1.0.0", sk.Metadata().Version)
	assert.Equal(t, "y-ai-agent-base", sk.Metadata().Author)
	assert.Equal(t, 80, sk.Metadata().EstimatedTokens)
}

func TestWeatherSkill_Match(t *testing.T) {
	sk := WeatherSkill()
	ctx := context.Background()

	score := sk.Match(ctx, "what is the weather in Tokyo")
	assert.Greater(t, score, float64(0))

	score = sk.Match(ctx, "will it rain tomorrow")
	assert.Greater(t, score, float64(0))

	score = sk.Match(ctx, "temperature forecast for London")
	assert.Greater(t, score, float64(0))

	score = sk.Match(ctx, "what time is it")
	assert.Equal(t, float64(0), score)
}

func TestWeatherSkill_Tools(t *testing.T) {
	sk := WeatherSkill()
	tools := sk.Tools()
	assert.Len(t, tools, 1)
	assert.Equal(t, "get_weather", tools[0].Name())

	result, err := tools[0].Execute(context.Background(),
		[]byte(`{"city":"Paris","unit":"celsius"}`))
	require.NoError(t, err)
	assert.Contains(t, result, "Paris")
}

func TestAllSkills_Distinct(t *testing.T) {
	skills := []struct {
		name string
		sk   interface{ Name() string }
	}{
		{"time", TimeSkill()},
		{"echo", EchoSkill()},
		{"weather", WeatherSkill()},
	}

	names := make(map[string]bool)
	for _, s := range skills {
		names[s.name] = true
	}
	assert.Len(t, names, 3, "all builtin skills must have unique names")
}

func TestSkills_Metadata(t *testing.T) {
	timeSkill := TimeSkill()
	echoSkill := EchoSkill()
	weatherSkill := WeatherSkill()

	assert.Greater(t, timeSkill.Metadata().EstimatedTokens, 0)
	assert.Greater(t, echoSkill.Metadata().EstimatedTokens, 0)
	assert.Greater(t, weatherSkill.Metadata().EstimatedTokens, 0)

	assert.Contains(t, timeSkill.Metadata().Tags, "time")
	assert.Contains(t, echoSkill.Metadata().Tags, "echo")
	assert.Contains(t, weatherSkill.Metadata().Tags, "weather")
}
