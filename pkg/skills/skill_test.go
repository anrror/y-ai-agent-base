package skills_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anrror/y-ai-agent-base/pkg/skills"
	"github.com/anrror/y-ai-agent-base/pkg/skills/builtin"
)

// ---------------------------------------------------------------------------
// Skill interface / NewSkill builder tests
// ---------------------------------------------------------------------------

func TestNewSkill_SetsNameAndDefaults(t *testing.T) {
	// Given: a minimal skill with only name
	sk := skills.NewSkill("test-skill")

	// Then
	assert.Equal(t, "test-skill", sk.Name())
	assert.Empty(t, sk.Description())
	assert.Empty(t, sk.Instructions())
	assert.Empty(t, sk.Tools())
	assert.Equal(t, "0.1.0", sk.Metadata().Version)
}

func TestNewSkill_WithDescription(t *testing.T) {
	sk := skills.NewSkill(
		"s",
		skills.WithDescription("does something"),
	)

	assert.Equal(t, "does something", sk.Description())
}

func TestNewSkill_WithInstructions(t *testing.T) {
	sk := skills.NewSkill(
		"s",
		skills.WithInstructions("Use tool X to do Y"),
	)

	assert.Equal(t, "Use tool X to do Y", sk.Instructions())
}

func TestNewSkill_WithTags(t *testing.T) {
	sk := skills.NewSkill(
		"s",
		skills.WithTags("alpha", "beta", "gamma"),
	)

	assert.Equal(t, []string{"alpha", "beta", "gamma"}, sk.Metadata().Tags)
}

func TestNewSkill_WithCategory(t *testing.T) {
	sk := skills.NewSkill(
		"s",
		skills.WithCategory("utility"),
	)

	assert.Equal(t, "utility", sk.Metadata().Category)
}

func TestNewSkill_WithVersion(t *testing.T) {
	sk := skills.NewSkill(
		"s",
		skills.WithVersion("2.0.0"),
	)

	assert.Equal(t, "2.0.0", sk.Metadata().Version)
}

func TestNewSkill_WithAuthor(t *testing.T) {
	sk := skills.NewSkill(
		"s",
		skills.WithAuthor("dev-team"),
	)

	assert.Equal(t, "dev-team", sk.Metadata().Author)
}

func TestNewSkill_WithRequiredModels(t *testing.T) {
	sk := skills.NewSkill(
		"s",
		skills.WithRequiredModels("gpt-4", "gpt-4o"),
	)

	assert.Equal(t, []string{"gpt-4", "gpt-4o"}, sk.Metadata().RequiredModels)
}

func TestNewSkill_WithEstimatedTokens(t *testing.T) {
	sk := skills.NewSkill(
		"s",
		skills.WithEstimatedTokens(150),
	)

	assert.Equal(t, 150, sk.Metadata().EstimatedTokens)
}

func TestNewSkill_WithMatchFunc_OverridesDefault(t *testing.T) {
	// Given: a skill with custom match that always returns 0.99
	sk := skills.NewSkill(
		"s",
		skills.WithTags("irrelevant"),
		skills.WithMatchFunc(func(_ context.Context, _ string) float64 {
			return 0.99
		}),
	)

	// When
	score := sk.Match(context.Background(), "anything")

	// Then: custom match function is used instead of tag scoring
	assert.Equal(t, 0.99, score)
}

func TestNewSkill_WithMetadata_SetsAllFields(t *testing.T) {
	meta := skills.SkillMetadata{
		Tags:            []string{"x", "y"},
		Category:        "cat",
		Version:         "3.0.0",
		Author:          "team",
		RequiredModels:  []string{"m1"},
		EstimatedTokens: 200,
	}

	sk := skills.NewSkill("s", skills.WithMetadata(meta))

	assert.Equal(t, meta, sk.Metadata())
}

// ---------------------------------------------------------------------------
// Match scoring tests
// ---------------------------------------------------------------------------

func TestMatch_ExactTagInQuery_ScoresProportionally(t *testing.T) {
	// Given: a skill with tags ["time","date"]
	sk := skills.NewSkill(
		"clock",
		skills.WithTags("time", "date"),
	)

	// When: query contains "time" as a word
	score := sk.Match(context.Background(), "what time is it")

	// Then: 1 tag matched out of 2 = 0.5
	assert.InDelta(t, 0.5, score, 0.01)
}

func TestMatch_NoOverlap_ReturnsZero(t *testing.T) {
	// Given: a skill tagged "weather"
	sk := skills.NewSkill(
		"climate",
		skills.WithTags("weather"),
	)

	// When: query has no weather-related words
	score := sk.Match(context.Background(), "translate hello to spanish")

	// Then: no tag overlap
	assert.Equal(t, 0.0, score)
}

func TestMatch_AllTagsMatch_ReturnsOne(t *testing.T) {
	// Given: all tags appear in the query
	sk := skills.NewSkill(
		"s",
		skills.WithTags("hello", "world"),
	)

	// When
	score := sk.Match(context.Background(), "hello world")

	// Then
	assert.InDelta(t, 1.0, score, 0.01)
}

func TestMatch_PartialWord_DoesNotMatch(t *testing.T) {
	// Given: tag is "time", query word is "timeout"
	sk := skills.NewSkill(
		"s",
		skills.WithTags("time"),
	)

	// When: "timeout" is not an exact match for "time"
	score := sk.Match(context.Background(), "timeout")

	// Then: no exact match
	assert.Equal(t, 0.0, score)
}

func TestMatch_CaseInsensitive(t *testing.T) {
	sk := skills.NewSkill(
		"s",
		skills.WithTags("Time", "DATE"),
	)

	// When: query uses different casing
	score := sk.Match(context.Background(), "What TIME is it today's DATE")

	// Then: case-insensitive matching
	assert.InDelta(t, 1.0, score, 0.01)
}

func TestMatch_EmptyTags_ReturnsZero(t *testing.T) {
	sk := skills.NewSkill("s") // no tags

	score := sk.Match(context.Background(), "anything")

	assert.Equal(t, 0.0, score)
}

func TestMatch_MultiWordTag_MatchesExactly(t *testing.T) {
	// Given: a multi-word tag
	sk := skills.NewSkill(
		"s",
		skills.WithTags("machine learning"),
	)

	// When: "machine" and "learning" are separate words in query
	// The tag "machine learning" is one string; we match whole tags against query words.
	// "machine" != "machine learning", "learning" != "machine learning" → no match
	score := sk.Match(context.Background(), "tell me about machine learning")

	assert.Equal(t, 0.0, score)
}

// ---------------------------------------------------------------------------
// Registry tests
// ---------------------------------------------------------------------------

func TestRegistry_RegisterAndGet(t *testing.T) {
	// Given
	reg := skills.NewRegistry()
	sk := skills.NewSkill("test-skill", skills.WithDescription("a skill"))

	// When
	err := reg.Register(sk)
	require.NoError(t, err)

	// Then
	got, ok := reg.Get("test-skill")
	require.True(t, ok)
	assert.Equal(t, "test-skill", got.Name())
	assert.Equal(t, "a skill", got.Description())
}

func TestRegistry_Get_NotFound(t *testing.T) {
	reg := skills.NewRegistry()

	_, ok := reg.Get("nonexistent")

	assert.False(t, ok)
}

func TestRegistry_Register_DuplicateName_ReturnsError(t *testing.T) {
	reg := skills.NewRegistry()
	sk := skills.NewSkill("dup")

	require.NoError(t, reg.Register(sk))
	err := reg.Register(sk)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "already registered")
}

func TestRegistry_List_ReturnsAll(t *testing.T) {
	reg := skills.NewRegistry()

	require.NoError(t, reg.Register(skills.NewSkill("a")))
	require.NoError(t, reg.Register(skills.NewSkill("b")))
	require.NoError(t, reg.Register(skills.NewSkill("c")))

	list := reg.List()
	assert.Len(t, list, 3)

	names := make(map[string]bool)
	for _, s := range list {
		names[s.Name()] = true
	}
	assert.True(t, names["a"])
	assert.True(t, names["b"])
	assert.True(t, names["c"])
}

func TestRegistry_Unregister_RemovesSkill(t *testing.T) {
	reg := skills.NewRegistry()
	require.NoError(t, reg.Register(skills.NewSkill("removable")))

	err := reg.Unregister("removable")
	require.NoError(t, err)

	_, ok := reg.Get("removable")
	assert.False(t, ok)
}

func TestRegistry_Unregister_NotFound_ReturnsError(t *testing.T) {
	reg := skills.NewRegistry()

	err := reg.Unregister("nonexistent")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestRegistry_Count_ReflectsOperations(t *testing.T) {
	reg := skills.NewRegistry()
	assert.Equal(t, 0, reg.Count())

	require.NoError(t, reg.Register(skills.NewSkill("a")))
	assert.Equal(t, 1, reg.Count())

	require.NoError(t, reg.Register(skills.NewSkill("b")))
	assert.Equal(t, 2, reg.Count())

	require.NoError(t, reg.Unregister("a"))
	assert.Equal(t, 1, reg.Count())
}

func TestRegistry_ConcurrentAccess(t *testing.T) {
	reg := skills.NewRegistry()
	var wg sync.WaitGroup
	const count = 20

	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			_ = reg.Register(skills.NewSkill(fmt.Sprintf("skill-%d", id)))
		}(i)
	}

	wg.Wait()
	assert.Equal(t, count, reg.Count())
}

// ---------------------------------------------------------------------------
// Registry Match tests
// ---------------------------------------------------------------------------

func TestRegistry_Match_ReturnsRelevantSkills(t *testing.T) {
	// Given: a registry with time and weather skills
	reg := skills.NewRegistry()

	timeSk := skills.NewSkill(
		"time",
		skills.WithTags("time", "date", "clock"),
		skills.WithDescription("time skill"),
	)
	weatherSk := skills.NewSkill(
		"weather",
		skills.WithTags("weather", "temperature", "forecast"),
		skills.WithDescription("weather skill"),
	)
	echoSk := skills.NewSkill(
		"echo",
		skills.WithTags("echo", "repeat"),
		skills.WithDescription("echo skill"),
	)

	require.NoError(t, reg.Register(timeSk))
	require.NoError(t, reg.Register(weatherSk))
	require.NoError(t, reg.Register(echoSk))

	// When: query about time
	results := reg.Match(context.Background(), "what time is it")

	// Then: time skill is returned, weather/echo are not
	require.Len(t, results, 1)
	assert.Equal(t, "time", results[0].Skill.Name())
	assert.InDelta(t, 0.33, results[0].Score, 0.02)
}

func TestRegistry_Match_ReturnsSortedByScore(t *testing.T) {
	// Given: multiple skills with varying tag overlap
	reg := skills.NewRegistry()

	skA := skills.NewSkill("a", skills.WithTags("alpha", "beta", "gamma"))
	skB := skills.NewSkill("b", skills.WithTags("alpha", "beta"))
	skC := skills.NewSkill("c", skills.WithTags("alpha"))

	require.NoError(t, reg.Register(skA))
	require.NoError(t, reg.Register(skB))
	require.NoError(t, reg.Register(skC))

	// When: query contains "alpha" only
	results := reg.Match(context.Background(), "alpha")

	// Then: sorted by score descending (higher = fewer tags = larger fraction)
	require.Len(t, results, 3)
	assert.Equal(t, "c", results[0].Skill.Name()) // 1/1 = 1.0
	assert.Equal(t, "b", results[1].Skill.Name()) // 1/2 = 0.5
	assert.Equal(t, "a", results[2].Skill.Name()) // 1/3 = 0.33
}

func TestRegistry_Match_NoMatch_ReturnsEmpty(t *testing.T) {
	reg := skills.NewRegistry()

	require.NoError(t, reg.Register(skills.NewSkill("x", skills.WithTags("foo"))))

	results := reg.Match(context.Background(), "unrelated query")

	assert.Empty(t, results)
}

func TestRegistry_Match_EmptyRegistry_ReturnsEmpty(t *testing.T) {
	reg := skills.NewRegistry()

	results := reg.Match(context.Background(), "anything")

	assert.Empty(t, results)
}

// ---------------------------------------------------------------------------
// Built-in skills tests
// ---------------------------------------------------------------------------

func TestTimeSkill_NameAndTags(t *testing.T) {
	sk := builtin.TimeSkill()

	assert.Equal(t, "time", sk.Name())
	assert.NotEmpty(t, sk.Description())
	assert.NotEmpty(t, sk.Instructions())
	assert.Len(t, sk.Tools(), 1)
	assert.Equal(t, "get_current_time", sk.Tools()[0].Name())

	meta := sk.Metadata()
	assert.Contains(t, meta.Tags, "time")
	assert.Contains(t, meta.Tags, "date")
	assert.Contains(t, meta.Tags, "clock")
	assert.Equal(t, "utility", meta.Category)
	assert.Equal(t, "1.0.0", meta.Version)
	assert.Equal(t, "y-ai-agent-base", meta.Author)
	assert.Greater(t, meta.EstimatedTokens, 0)
}

func TestTimeSkill_Match_TimeQuery_ScoresPositive(t *testing.T) {
	sk := builtin.TimeSkill()

	score := sk.Match(context.Background(), "what time is it")

	// TimeSkill has 5 tags (time, date, clock, now, today).
	// "what time is it" matches "time" → 1/5 = 0.2.
	assert.Greater(t, score, 0.0)
	assert.LessOrEqual(t, score, 0.5)
}

func TestEchoSkill_NameAndTags(t *testing.T) {
	sk := builtin.EchoSkill()

	assert.Equal(t, "echo", sk.Name())
	assert.Len(t, sk.Tools(), 1)
	assert.Equal(t, "echo", sk.Tools()[0].Name())

	meta := sk.Metadata()
	assert.Contains(t, meta.Tags, "echo")
	assert.Contains(t, meta.Tags, "repeat")
	assert.Contains(t, meta.Tags, "mirror")
}

func TestWeatherSkill_NameAndTags(t *testing.T) {
	sk := builtin.WeatherSkill()

	assert.Equal(t, "weather", sk.Name())
	assert.Len(t, sk.Tools(), 1)
	assert.Equal(t, "get_weather", sk.Tools()[0].Name())

	meta := sk.Metadata()
	assert.Contains(t, meta.Tags, "weather")
	assert.Contains(t, meta.Tags, "temperature")
	assert.Contains(t, meta.Tags, "forecast")
}

func TestWeatherSkill_Match_UnrelatedQuery_ReturnsZero(t *testing.T) {
	sk := builtin.WeatherSkill()

	score := sk.Match(context.Background(), "translate hello")

	assert.Equal(t, 0.0, score)
}

func TestBuiltinSkills_TimeOverWeather_ForTimeQuery(t *testing.T) {
	timeSk := builtin.TimeSkill()
	weatherSk := builtin.WeatherSkill()

	query := "what time is it right now"

	timeScore := timeSk.Match(context.Background(), query)
	weatherScore := weatherSk.Match(context.Background(), query)

	// Time skill should score higher for a time query
	assert.Greater(t, timeScore, weatherScore)
}

func TestBuiltinSkills_WeatherOverTime_ForWeatherQuery(t *testing.T) {
	timeSk := builtin.TimeSkill()
	weatherSk := builtin.WeatherSkill()

	query := "what is the weather forecast today"

	timeScore := timeSk.Match(context.Background(), query)
	weatherScore := weatherSk.Match(context.Background(), query)

	// Weather skill should score higher for a weather query
	assert.Greater(t, weatherScore, timeScore)
}

// ---------------------------------------------------------------------------
// SortMatchResults tests
// ---------------------------------------------------------------------------

func TestSortMatchResults_SortsDescending(t *testing.T) {
	skA := skills.NewSkill("a")
	skB := skills.NewSkill("b")
	skC := skills.NewSkill("c")

	results := []skills.MatchResult{
		{Skill: skA, Score: 0.3},
		{Skill: skB, Score: 0.9},
		{Skill: skC, Score: 0.6},
	}

	skills.SortMatchResults(results)

	assert.Equal(t, "b", results[0].Skill.Name())
	assert.Equal(t, "c", results[1].Skill.Name())
	assert.Equal(t, "a", results[2].Skill.Name())
}

func TestSortMatchResults_SameScore_SortsByName(t *testing.T) {
	skB := skills.NewSkill("beta")
	skA := skills.NewSkill("alpha")
	skG := skills.NewSkill("gamma")

	results := []skills.MatchResult{
		{Skill: skB, Score: 0.5},
		{Skill: skA, Score: 0.5},
		{Skill: skG, Score: 0.5},
	}

	skills.SortMatchResults(results)

	assert.Equal(t, "alpha", results[0].Skill.Name())
	assert.Equal(t, "beta", results[1].Skill.Name())
	assert.Equal(t, "gamma", results[2].Skill.Name())
}
