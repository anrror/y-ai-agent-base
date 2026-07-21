// Package skills defines a pluggable skill system for AI agents.
// Each skill bundles tools with metadata, instructions, and query matching.
package skills

import (
	"context"
	"sort"
	"strings"

	"github.com/anrror/y-ai-agent-base/pkg/tool"
)

// Skill is a domain capability that bundles tools with instructions and metadata.
// Skills can be matched against user queries to determine relevance.
type Skill interface {
	Name() string
	Description() string
	Instructions() string
	Tools() []tool.Tool
	Metadata() SkillMetadata
	Match(ctx context.Context, query string) float64
}

// SkillMetadata holds descriptive and operational metadata for a skill.
type SkillMetadata struct {
	Tags            []string // Keywords used for query matching
	Category        string   // Logical grouping (e.g. "utility", "weather")
	Version         string   // Semantic version of the skill
	Author          string   // Skill author or maintainer
	RequiredModels  []string // Models needed (empty = any model)
	EstimatedTokens int      // Approximate token cost per invocation
}

// tagMatchScore computes a simple overlap score between a query string and
// a set of tags. The query is lowercased and split into words; each word is
// checked for exact (case-insensitive) match against each tag. Individual
// tags may themselves be multi-word.
//
// Score = matched-tags / total-tags. Returns 0 when there are no tags.
func tagMatchScore(query string, tags []string) float64 {
	if len(tags) == 0 {
		return 0
	}

	queryLower := strings.ToLower(query)
	queryWords := strings.Fields(queryLower)

	matched := 0
	for _, tag := range tags {
		tagLower := strings.ToLower(tag)
		for _, qw := range queryWords {
			if qw == tagLower {
				matched++
				break
			}
		}
	}

	return float64(matched) / float64(len(tags))
}

// ---------------------------------------------------------------------------
// Functional options builder
// ---------------------------------------------------------------------------

// SkillOption is a functional option for NewSkill.
type SkillOption func(*skillImpl)

type skillImpl struct {
	name         string
	description  string
	instructions string
	tools        []tool.Tool
	metadata     SkillMetadata
	matchFn      func(ctx context.Context, query string) float64
}

func (s *skillImpl) Name() string            { return s.name }
func (s *skillImpl) Description() string     { return s.description }
func (s *skillImpl) Instructions() string    { return s.instructions }
func (s *skillImpl) Tools() []tool.Tool      { return s.tools }
func (s *skillImpl) Metadata() SkillMetadata { return s.metadata }
func (s *skillImpl) Match(ctx context.Context, query string) float64 {
	if s.matchFn != nil {
		return s.matchFn(ctx, query)
	}
	return tagMatchScore(query, s.metadata.Tags)
}

// NewSkill creates a Skill with the given name and optional configuration.
// The default Match implementation uses tag-based overlap scoring.
func NewSkill(name string, opts ...SkillOption) Skill {
	s := &skillImpl{
		name: name,
		metadata: SkillMetadata{
			Version: "0.1.0",
		},
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// WithDescription sets the skill description.
func WithDescription(desc string) SkillOption {
	return func(s *skillImpl) { s.description = desc }
}

// WithInstructions sets the LLM instructions for the skill.
func WithInstructions(instr string) SkillOption {
	return func(s *skillImpl) { s.instructions = instr }
}

// WithTools attaches the given tools to the skill.
func WithTools(tools ...tool.Tool) SkillOption {
	return func(s *skillImpl) { s.tools = append(s.tools, tools...) }
}

// WithMetadata sets the full SkillMetadata.
func WithMetadata(m SkillMetadata) SkillOption {
	return func(s *skillImpl) { s.metadata = m }
}

// WithTags sets the metadata tags (overwrites, not appends).
func WithTags(tags ...string) SkillOption {
	return func(s *skillImpl) {
		s.metadata.Tags = append([]string(nil), tags...)
	}
}

// WithCategory sets the metadata category.
func WithCategory(cat string) SkillOption {
	return func(s *skillImpl) { s.metadata.Category = cat }
}

// WithVersion sets the metadata version.
func WithVersion(v string) SkillOption {
	return func(s *skillImpl) { s.metadata.Version = v }
}

// WithAuthor sets the metadata author.
func WithAuthor(a string) SkillOption {
	return func(s *skillImpl) { s.metadata.Author = a }
}

// WithRequiredModels sets the metadata required models.
func WithRequiredModels(models ...string) SkillOption {
	return func(s *skillImpl) { s.metadata.RequiredModels = append([]string(nil), models...) }
}

// WithEstimatedTokens sets the metadata estimated tokens.
func WithEstimatedTokens(n int) SkillOption {
	return func(s *skillImpl) { s.metadata.EstimatedTokens = n }
}

// WithMatchFunc overrides the default tag-based match function.
func WithMatchFunc(fn func(ctx context.Context, query string) float64) SkillOption {
	return func(s *skillImpl) { s.matchFn = fn }
}

// ---------------------------------------------------------------------------
// Match helpers
// ---------------------------------------------------------------------------

// MatchResult pairs a skill with its match score for ranking.
type MatchResult struct {
	Skill Skill
	Score float64
}

// SortMatchResults sorts results by score descending, then by name ascending.
func SortMatchResults(results []MatchResult) {
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].Skill.Name() < results[j].Skill.Name()
	})
}
