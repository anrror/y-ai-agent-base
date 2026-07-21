package knowledge

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// WebSearchRetriever searches the internet via a caller-provided function.
//
// Usage:
//
//	searcher := knowledge.NewWebSearchRetriever(
//	    func(ctx context.Context, query string) ([]*knowledge.Result, error) {
//	        // Call Google Custom Search, Bing, Brave, SearXNG, etc.
//	        results := // ... parse response into []*knowledge.Result
//	        return results, nil
//	    },
//	)
//
// Each Result should have:
//   - ID:   the page URL (unique)
//   - Content: the snippet / summary
//   - Metadata: {"title": ..., "url": ..., "source": "web"}
type WebSearchRetriever struct {
	searchFn func(ctx context.Context, query string) ([]*Result, error)
}

// NewWebSearchRetriever creates a WebSearchRetriever.
// The searchFn receives the user query and returns search results.
// When searchFn is nil, the retriever returns empty results.
func NewWebSearchRetriever(searchFn func(ctx context.Context, query string) ([]*Result, error)) *WebSearchRetriever {
	return &WebSearchRetriever{searchFn: searchFn}
}

// NewDefaultWebSearchRetriever creates a WebSearchRetriever that uses
// a configurable HTTP search endpoint. The endpoint must return HTML
// search results (simple scraper). For production use, pass a custom
// searchFn that calls a structured search API instead.
//
// Deprecated: This is a basic scraper for demonstration. Use
// NewWebSearchRetriever with a proper search API client.
func NewDefaultWebSearchRetriever(client *http.Client, searchURL string) *WebSearchRetriever {
	if client == nil {
		client = http.DefaultClient
	}
	return NewWebSearchRetriever(func(ctx context.Context, query string) ([]*Result, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL+"?q="+query, nil)
		if err != nil {
			return nil, fmt.Errorf("websearch: create request: %w", err)
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("websearch: request failed: %w", err)
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("websearch: read body: %w", err)
		}

		// Simple title extraction from HTML (rough, for demo only)
		text := string(body)
		title := extractTitle(text)

		docID := fmt.Sprintf("web_%x", sha256.Sum256([]byte(searchURL+"?q="+query)))[:24]
		return []*Result{
			{
				Document: Document{
					ID:      docID,
					Content: truncate(text, 2000),
					Metadata: map[string]any{
						"title":  title,
						"url":    searchURL,
						"source": "web",
					},
				},
				Score: 1.0,
			},
		}, nil
	})
}

func (r *WebSearchRetriever) ID() string { return "web_search" }

func (r *WebSearchRetriever) Retrieve(ctx context.Context, query string, opts ...SearchOption) ([]*Result, error) {
	if r.searchFn == nil {
		return nil, nil
	}
	results, err := r.searchFn(ctx, query)
	if err != nil {
		return nil, err
	}
	// Apply filtering options.
	params := applySearchOptions(opts)
	filtered := filterResults(results, params)
	return filtered, nil
}

// WebFetchRetriever fetches the content of a URL and returns it as a
// knowledge document. The fetch function is caller-provided so the
// framework remains agnostic to HTTP clients and auth.
type WebFetchRetriever struct {
	fetchFn func(ctx context.Context, url string) (string, error)
}

// NewWebFetchRetriever creates a WebFetchRetriever.
// The fetchFn receives a URL and returns the page text content.
// When fetchFn is nil, the retriever returns empty results.
func NewWebFetchRetriever(fetchFn func(ctx context.Context, url string) (string, error)) *WebFetchRetriever {
	return &WebFetchRetriever{fetchFn: fetchFn}
}

// NewDefaultWebFetchRetriever creates a WebFetchRetriever with a default
// HTTP-based fetch implementation.
func NewDefaultWebFetchRetriever(client *http.Client) *WebFetchRetriever {
	if client == nil {
		client = http.DefaultClient
	}
	return NewWebFetchRetriever(func(ctx context.Context, url string) (string, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return "", fmt.Errorf("webfetch: create request: %w", err)
		}
		resp, err := client.Do(req)
		if err != nil {
			return "", fmt.Errorf("webfetch: request failed: %w", err)
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", fmt.Errorf("webfetch: read body: %w", err)
		}

		// Strip HTML tags for clean text.
		text := stripHTMLTags(string(body))
		return truncate(text, 5000), nil
	})
}

// FetchURL is a convenience method that fetches a single URL and returns
// a Result. It is used by the fetch_url tool.
func (r *WebFetchRetriever) FetchURL(ctx context.Context, url string) (*Result, error) {
	if r.fetchFn == nil {
		return nil, nil
	}
	content, err := r.fetchFn(ctx, url)
	if err != nil {
		return nil, err
	}
	return &Result{
		Document: Document{
			ID:      url,
			Content: content,
			Metadata: map[string]any{
				"url":    url,
				"source": "web",
			},
		},
		Score: 1.0,
	}, nil
}

func (r *WebFetchRetriever) ID() string { return "web_fetch" }

func (r *WebFetchRetriever) Retrieve(ctx context.Context, query string, opts ...SearchOption) ([]*Result, error) {
	// WebFetchRetriever treats the query as a URL to fetch.
	if r.fetchFn == nil || query == "" {
		return nil, nil
	}
	result, err := r.FetchURL(ctx, query)
	if err != nil {
		return nil, err
	}
	return []*Result{result}, nil
}

// --- helpers ---------------------------------------------------------------

func extractTitle(html string) string {
	idx := strings.Index(strings.ToLower(html), "<title")
	if idx < 0 {
		return ""
	}
	start := strings.Index(html[idx:], ">")
	if start < 0 {
		return ""
	}
	start += idx + 1
	end := strings.Index(html[start:], "</title")
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(html[start : start+end])
}

func stripHTMLTags(s string) string {
	var b strings.Builder
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
		case !inTag:
			b.WriteRune(r)
		}
	}
	// Collapse whitespace.
	result := strings.Fields(b.String())
	return strings.Join(result, " ")
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func filterResults(results []*Result, params SearchParams) []*Result {
	filtered := make([]*Result, 0, len(results))
	for _, r := range results {
		if r.Score < params.Threshold {
			continue
		}
		filtered = append(filtered, r)
	}
	sortResults(filtered)
	if len(filtered) > params.TopK {
		filtered = filtered[:params.TopK]
	}
	return filtered
}

// compile-time interface checks.
var (
	_ Retriever = (*WebSearchRetriever)(nil)
	_ Retriever = (*WebFetchRetriever)(nil)
	_ Retriever = (*StoreRetriever)(nil)
)
