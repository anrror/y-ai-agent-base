package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anrror/y-ai-agent-base/pkg/component"
	"github.com/anrror/y-ai-agent-base/pkg/pipeline"
	"github.com/anrror/y-ai-agent-base/pkg/tool"
	"github.com/anrror/y-ai-agent-base/pkg/types"
)

// ComponentID is the extension/component identifier used to register
// a Knowledge instance on an Agent.
const ComponentID = "knowledge"

// Config controls the behaviour of the Knowledge component.
type Config struct {
	// AutoInject, when true, causes the Knowledge component to
	// automatically retrieve relevant documents and inject them as
	// a system-context message before every Chat() / Run() call.
	AutoInject bool `json:"auto_inject"`

	// TopK is the number of top results to inject (default 3).
	// Only meaningful when AutoInject is true.
	TopK int `json:"top_k"`

	// Threshold is the minimum similarity score [0,1] for auto-injected
	// results. 0 means no filtering (default).
	Threshold float64 `json:"threshold"`
}

// DefaultConfig returns a Config with sensible defaults:
// AutoInject disabled (manual mode), TopK=3.
func DefaultConfig() Config {
	return Config{
		AutoInject: false,
		TopK:       3,
		Threshold:  0.0,
	}
}

// Knowledge is a pluggable Agent Extension + Component that provides
// knowledge retrieval from multiple sources (local Store, web search,
// URL fetch) and optionally injects relevant knowledge into the
// pipeline via middleware.
//
// Each Agent that needs knowledge attaches it via Builder.WithKnowledge().
// When not attached, Agent.Knowledge() returns nil — the agent runs
// without any knowledge capability.
//
// Knowledge can expose agent-callable tools (search_web, fetch_url,
// search_knowledge) that the LLM autonomously invokes during conversation.
type Knowledge struct {
	retrievers []Retriever
	pipe       pipeline.Pipeline
	cfg        Config
}

// New creates a Knowledge component with a backing Store.
// The Store is wrapped as a StoreRetriever and included in searches.
// Pass nil for store if you only use custom retrievers.
//
// Example:
//
//	kn := knowledge.New(myStore, knowledge.DefaultConfig())
func New(store Store, cfg Config) *Knowledge {
	if cfg.TopK <= 0 {
		cfg.TopK = 3
	}
	k := &Knowledge{cfg: cfg}
	if store != nil {
		k.retrievers = append(k.retrievers, NewStoreRetriever("store", store))
	}
	return k
}

// NewWithRetrievers creates a Knowledge component with the given
// retrievers. No Store is attached — use WithRetrievers or the
// individual retriever constructors.
//
// Example:
//
//	kn := knowledge.NewWithRetrievers(cfg,
//	    knowledge.NewStoreRetriever("docs", myStore),
//	    webSearchRetriever,
//	)
func NewWithRetrievers(cfg Config, retrievers ...Retriever) *Knowledge {
	if cfg.TopK <= 0 {
		cfg.TopK = 3
	}
	rs := make([]Retriever, len(retrievers))
	copy(rs, retrievers)
	return &Knowledge{retrievers: rs, cfg: cfg}
}

// ID returns the component identifier "knowledge".
func (k *Knowledge) ID() string { return ComponentID }

// Close releases resources held by the Knowledge component.
func (k *Knowledge) Close() error {
	var errs []string
	for _, r := range k.retrievers {
		if closer, ok := r.(interface{ Close() error }); ok {
			if err := closer.Close(); err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", r.ID(), err))
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("knowledge: close errors: %s", strings.Join(errs, "; "))
	}
	return nil
}

// Init implements component.Component. It captures the pipeline
// reference for middleware injection when AutoInject is enabled.
func (k *Knowledge) Init(ctx *component.InitContext) error {
	k.pipe = ctx.Pipeline
	return nil
}

// --- retriever management --------------------------------------------------

// Retrievers returns all registered retrievers.
func (k *Knowledge) Retrievers() []Retriever {
	out := make([]Retriever, len(k.retrievers))
	copy(out, k.retrievers)
	return out
}

// WithRetrievers appends additional retrievers to the component.
func (k *Knowledge) WithRetrievers(rs ...Retriever) *Knowledge {
	k.retrievers = append(k.retrievers, rs...)
	return k
}

// Store returns the first Store found among the retrievers, or nil.
// This is a convenience method for backward compatibility.
func (k *Knowledge) Store() Store {
	for _, r := range k.retrievers {
		if sr, ok := r.(*StoreRetriever); ok {
			return sr.store
		}
	}
	return nil
}

// --- public API -----------------------------------------------------------

// Search queries ALL registered retrievers and merges the results.
// Results are deduplicated by ID (highest score wins) and sorted by
// descending score.
//
// Returns empty slice when no retrievers are registered.
func (k *Knowledge) Search(ctx context.Context, query string, opts ...SearchOption) ([]*Result, error) {
	if len(k.retrievers) == 0 {
		return nil, nil
	}

	// For a single retriever, delegate directly.
	if len(k.retrievers) == 1 {
		return k.retrievers[0].Retrieve(ctx, query, opts...)
	}

	// Multiple retrievers → fan-out via HybridRetriever.
	h := NewHybridRetriever("_search", k.retrievers...)
	return h.Retrieve(ctx, query, opts...)
}

// StoreDocs stores documents in the first StoreRetriever found.
// Returns an error when no store is attached.
func (k *Knowledge) StoreDocs(ctx context.Context, docs ...*Document) error {
	store := k.Store()
	if store == nil {
		return fmt.Errorf("knowledge: no Store retriever attached")
	}
	return store.Store(ctx, docs...)
}

// DeleteDocs deletes documents from the first StoreRetriever found.
func (k *Knowledge) DeleteDocs(ctx context.Context, ids ...string) error {
	store := k.Store()
	if store == nil {
		return nil
	}
	return store.Delete(ctx, ids...)
}

// --- tools -----------------------------------------------------------------

// Tools returns agent-callable tools backed by this Knowledge component.
//
// The tools allow the LLM to autonomously search the web, fetch URLs,
// and query the knowledge base during conversation.
//
// Tools are registered automatically when the Knowledge component is
// attached via Builder.WithKnowledge() and the ToolProvider interface
// is detected.
func (k *Knowledge) Tools() []tool.Tool {
	tools := make([]tool.Tool, 0, 3)

	if k.hasRetrieverType("*knowledge.WebSearchRetriever") {
		tools = append(tools, k.searchWebTool())
	}
	if k.hasRetrieverType("*knowledge.WebFetchRetriever") {
		tools = append(tools, k.fetchURLTool())
	}
	tools = append(tools, k.searchKnowledgeTool())

	return tools
}

func (k *Knowledge) searchWebTool() tool.Tool {
	return tool.FromFunction(
		"search_web",
		"Searches the internet for information matching the query. Use this to find current information, news, documentation, or any topic that may not be in your training data.",
		func(ctx context.Context, args json.RawMessage) (string, error) {
			var params struct {
				Query string `json:"query"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return "", fmt.Errorf("%w: %w", tool.ErrInvalidArgs, err)
			}
			if params.Query == "" {
				return "", fmt.Errorf("%w: query is required", tool.ErrInvalidArgs)
			}

			// Find the WebSearchRetriever.
			for _, r := range k.retrievers {
				if ws, ok := r.(*WebSearchRetriever); ok {
					results, err := ws.Retrieve(ctx, params.Query, WithTopK(5))
					if err != nil {
						return "", err
					}
					return formatJSONResults(results)
				}
			}
			return `{"results":[]}`, nil
		},
		tool.NewParamSchema().
			AddString("query", "The search query", true).
			Build(),
	)
}

func (k *Knowledge) fetchURLTool() tool.Tool {
	return tool.FromFunction(
		"fetch_url",
		"Fetches the content of a URL and returns the page text. Use this to read specific web pages, documentation, or articles.",
		func(ctx context.Context, args json.RawMessage) (string, error) {
			var params struct {
				URL string `json:"url"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return "", fmt.Errorf("%w: %w", tool.ErrInvalidArgs, err)
			}
			if params.URL == "" {
				return "", fmt.Errorf("%w: url is required", tool.ErrInvalidArgs)
			}

			for _, r := range k.retrievers {
				if wf, ok := r.(*WebFetchRetriever); ok {
					result, err := wf.FetchURL(ctx, params.URL)
					if err != nil {
						return "", err
					}
					return formatJSONResults([]*Result{result})
				}
			}
			return `{"error":"no web fetch retriever configured"}`, nil
		},
		tool.NewParamSchema().
			AddString("url", "The URL to fetch", true).
			Build(),
	)
}

func (k *Knowledge) searchKnowledgeTool() tool.Tool {
	return tool.FromFunction(
		"search_knowledge",
		"Searches all available knowledge sources (local documents, web, etc.) for information relevant to the query.",
		func(ctx context.Context, args json.RawMessage) (string, error) {
			var params struct {
				Query string `json:"query"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return "", fmt.Errorf("%w: %w", tool.ErrInvalidArgs, err)
			}
			if params.Query == "" {
				return "", fmt.Errorf("%w: query is required", tool.ErrInvalidArgs)
			}

			results, err := k.Search(ctx, params.Query)
			if err != nil {
				return "", err
			}
			return formatJSONResults(results)
		},
		tool.NewParamSchema().
			AddString("query", "The search query", true).
			Build(),
	)
}

// --- middleware (AutoInject) ----------------------------------------------

// knowledgeCtxKey is used to attach search results to the pipeline context.
type knowledgeCtxKey struct{}

// ContextResults returns the knowledge search results stored in ctx by
// the AutoInject middleware, or nil.
func ContextResults(ctx context.Context) []*Result {
	v, _ := ctx.Value(knowledgeCtxKey{}).([]*Result)
	return v
}

// Middleware implements agent.MiddlewareProvider.
//
// When AutoInject is enabled, this middleware:
//  1. Searches ALL registered retrievers with the last user message
//  2. Attaches results to the context (for downstream inspection)
//  3. Prepends a system message with the retrieved knowledge context
//
// When AutoInject is disabled or no retrievers are registered,
// the middleware is a no-op pass-through.
func (k *Knowledge) Middleware() pipeline.Middleware {
	if !k.cfg.AutoInject || len(k.retrievers) == 0 {
		return func(next pipeline.Handler) pipeline.Handler {
			return next
		}
	}

	return func(next pipeline.Handler) pipeline.Handler {
		return func(ctx context.Context, input *types.ChatInput, output *types.ChatOutput) error {
			query := lastUserMessage(input.Messages)
			if query == "" {
				return next(ctx, input, output)
			}

			results, err := k.Search(ctx, query, WithTopK(k.cfg.TopK), WithThreshold(k.cfg.Threshold))
			if err != nil || len(results) == 0 {
				return next(ctx, input, output)
			}

			ctx = context.WithValue(ctx, knowledgeCtxKey{}, results)

			knowledgeText := formatResults(results)
			knowledgeMsg := types.Message{
				Role:    "system",
				Content: fmt.Sprintf("The following knowledge may be relevant to the user's query:\n\n%s", knowledgeText),
			}

			insertAt := 0
			if len(input.Messages) > 0 && input.Messages[0].Role == "system" {
				insertAt = 1
			}
			msgs := make([]types.Message, 0, len(input.Messages)+1)
			msgs = append(msgs, input.Messages[:insertAt]...)
			msgs = append(msgs, knowledgeMsg)
			msgs = append(msgs, input.Messages[insertAt:]...)
			input.Messages = msgs

			return next(ctx, input, output)
		}
	}
}

// --- helpers --------------------------------------------------------------

// lastUserMessage returns the Content of the last message with role "user".
func lastUserMessage(msgs []types.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			return msgs[i].Content
		}
	}
	return ""
}

// formatResults renders search results as a readable text block.
func formatResults(results []*Result) string {
	var b strings.Builder
	for i, r := range results {
		source := "document"
		if r.Metadata != nil {
			if s, ok := r.Metadata["source"].(string); ok {
				source = s
			}
		}
		b.WriteString(fmt.Sprintf("[%d] (%s, score: %.4f)\n%s\n", i+1, source, r.Score, r.Content))
		if i < len(results)-1 {
			b.WriteString("---\n")
		}
	}
	return b.String()
}

// formatJSONResults marshals results into a JSON string for tool responses.
func formatJSONResults(results []*Result) (string, error) {
	if results == nil {
		results = []*Result{}
	}
	data, err := json.Marshal(map[string]any{"results": results})
	if err != nil {
		return "", fmt.Errorf("knowledge: marshal results: %w", err)
	}
	return string(data), nil
}

// hasRetrieverType checks whether any registered retriever matches the
// given fully-qualified type name.
func (k *Knowledge) hasRetrieverType(typeName string) bool {
	for _, r := range k.retrievers {
		if fmt.Sprintf("%T", r) == typeName {
			return true
		}
	}
	return false
}

// compile-time interface checks.
var (
	_ component.Component = (*Knowledge)(nil)
)
