// Package openai implements the OpenAI-compatible provider for LLM,
// embedding, and guard (moderation) services.
package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/anrror/y-ai-agent-base/pkg/provider"
	"github.com/anrror/y-ai-agent-base/pkg/tool"
	"github.com/anrror/y-ai-agent-base/pkg/types"
)

const (
	defaultHTTPTimeout  = 60 * time.Second
	defaultPingTimeout  = 5 * time.Second
	defaultDegradeAfter = 3 // consecutive failures before circuit opens
)

// OpenAIProvider implements LLMProvider, EmbeddingProvider, GuardProvider, and Provider.
type OpenAIProvider struct {
	baseURL           string
	apiKey            string
	model             string
	httpClient        *http.Client
	tools             []tool.Tool
	health            provider.ProviderHealth
	mu                sync.RWMutex
	degradedThreshold int
}

// NewOpenAIProvider creates an OpenAI-compatible provider from a ProviderConfig.
// The BaseURL is normalized by stripping any trailing slash.
func NewOpenAIProvider(cfg *provider.ProviderConfig) *OpenAIProvider {
	return &OpenAIProvider{
		baseURL:           strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:            cfg.APIKey,
		model:             cfg.Model,
		httpClient:        &http.Client{Timeout: defaultHTTPTimeout},
		health:            provider.ProviderHealth{Status: provider.StatusHealthy},
		degradedThreshold: defaultDegradeAfter,
	}
}

// NewProviderSet creates a ProviderSet with optional per-role OpenAI providers.
// Each role uses an independent provider instance configured via its own ProviderConfig.
// Pass nil for any role to disable that capability.
func NewProviderSet(chatCfg, embedCfg, guardCfg *provider.ProviderConfig) *provider.ProviderSet {
	ps := &provider.ProviderSet{}
	if chatCfg != nil {
		ps.Chat = NewOpenAIProvider(chatCfg)
	}
	if embedCfg != nil {
		ps.Embedding = NewOpenAIProvider(embedCfg)
	}
	if guardCfg != nil {
		ps.Guard = NewOpenAIProvider(guardCfg)
	}
	return ps
}

// Name returns the provider identifier.
func (p *OpenAIProvider) Name() string {
	return "openai"
}

// Models returns the configured model name.
func (p *OpenAIProvider) Models() []string {
	if p.model == "" {
		return nil
	}
	return []string{p.model}
}

// Close releases any underlying resources. Currently a no-op.
func (p *OpenAIProvider) Close() error {
	return nil
}

// SetTools sets the tools that will be sent to the LLM on subsequent
// Chat / ChatStream calls. Pass nil to clear.
func (p *OpenAIProvider) SetTools(tools []tool.Tool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.tools = tools
}

// ── Health & circuit breaker ──────────────────────────────

// Ping checks provider reachability via GET /v1/models with a short timeout.
// A successful ping resets the health status to healthy.
func (p *OpenAIProvider) Ping(ctx context.Context) error {
	pingCtx, cancel := context.WithTimeout(ctx, defaultPingTimeout)
	defer cancel()

	resp, err := p.do(pingCtx, "GET", "/v1/models", nil)
	if err != nil {
		p.recordFailure(err)
		return fmt.Errorf("openai ping: %w", err)
	}
	_ = resp.Body.Close()

	p.recordSuccess()
	return nil
}

// checkHealth returns an error when the circuit breaker is open.
// When degraded, it attempts a recovery ping before rejecting.
func (p *OpenAIProvider) checkHealth(ctx context.Context) error {
	p.mu.Lock()
	status := p.health.Status
	p.mu.Unlock()

	switch status {
	case provider.StatusHealthy:
		return nil
	case provider.StatusDegraded:
		// Attempt recovery ping before rejecting.
		if err := p.Ping(ctx); err != nil {
			return types.ErrProviderUnavailable
		}
		return nil
	case provider.StatusUnavailable:
		return types.ErrProviderUnavailable
	default:
		return types.ErrProviderUnavailable
	}
}

// recordSuccess resets the failure counter and sets status to healthy.
func (p *OpenAIProvider) recordSuccess() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.health.Status = provider.StatusHealthy
	p.health.ConsecutiveFailures = 0
	p.health.LastError = ""
	p.health.LastChecked = time.Now()
}

// recordFailure increments the failure counter and, if the threshold is exceeded,
// opens the circuit breaker (moves to degraded).
func (p *OpenAIProvider) recordFailure(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.health.ConsecutiveFailures++
	p.health.LastError = err.Error()
	p.health.LastChecked = time.Now()
	if p.health.ConsecutiveFailures >= p.degradedThreshold {
		p.health.Status = provider.StatusDegraded
	}
}

// Health returns a copy of the current health state (thread-safe).
func (p *OpenAIProvider) Health() provider.ProviderHealth {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.health
}

// Chat sends a conversation and returns the full response text.
// When the LLM returns tool_calls instead of content, an empty string is returned
// — consumers should use ChatStream for tool-calling workflows.
func (p *OpenAIProvider) Chat(ctx context.Context, messages []types.Message, config types.ModelConfig) (string, error) {
	if err := p.checkHealth(ctx); err != nil {
		return "", err
	}

	reqBody := chatRequest(messages, config, false, p.buildOpenAITools())
	resp, err := p.do(ctx, "POST", "/v1/chat/completions", reqBody)
	if err != nil {
		p.recordFailure(err)
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		err := readError(resp)
		p.recordFailure(err)
		return "", err
	}

	var result chatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		p.recordFailure(err)
		return "", fmt.Errorf("decode chat response: %w", err)
	}

	if len(result.Choices) == 0 {
		err := fmt.Errorf("openai: no choices in chat response")
		p.recordFailure(err)
		return "", err
	}

	p.recordSuccess()
	return result.Choices[0].Message.Content, nil
}

// ChatStream sends a conversation and returns a channel of streamed events.
// The channel is closed when the stream completes or ctx is cancelled.
// StreamEvents may carry ToolCalls when the model requests tool invocations.
func (p *OpenAIProvider) ChatStream(ctx context.Context, messages []types.Message, config types.ModelConfig) (<-chan types.StreamEvent, error) {
	if err := p.checkHealth(ctx); err != nil {
		return nil, err
	}

	reqBody := chatRequest(messages, config, true, p.buildOpenAITools())
	resp, err := p.do(ctx, "POST", "/v1/chat/completions", reqBody)
	if err != nil {
		p.recordFailure(err)
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		defer func() { _ = resp.Body.Close() }()
		err := readError(resp)
		p.recordFailure(err)
		return nil, err
	}

	p.recordSuccess()
	events := make(chan types.StreamEvent)
	// Use context.WithoutCancel so that Timeout middleware's deferred cancel() won't
	// kill readSSE before it reads any SSE data from the HTTP response body.
	go p.readSSE(context.WithoutCancel(ctx), resp.Body, events)
	return events, nil
}

// Embed generates a vector embedding for the given text.
func (p *OpenAIProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	if err := p.checkHealth(ctx); err != nil {
		return nil, err
	}

	reqBody := struct {
		Model string `json:"model"`
		Input string `json:"input"`
	}{
		Model: p.model,
		Input: text,
	}

	resp, err := p.do(ctx, "POST", "/v1/embeddings", reqBody)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, readError(resp)
	}

	var result embeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode embedding response: %w", err)
	}

	if len(result.Data) == 0 {
		return nil, fmt.Errorf("openai: no embedding data returned")
	}

	return result.Data[0].Embedding, nil
}

// Check evaluates whether the text violates safety policies.
// Returns true when content is allowed.
func (p *OpenAIProvider) Check(ctx context.Context, text string) (bool, error) {
	if err := p.checkHealth(ctx); err != nil {
		return false, err
	}

	reqBody := struct {
		Input string `json:"input"`
	}{
		Input: text,
	}

	resp, err := p.do(ctx, "POST", "/v1/moderations", reqBody)
	if err != nil {
		return false, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return false, readError(resp)
	}

	var result moderationResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false, fmt.Errorf("decode moderation response: %w", err)
	}

	if len(result.Results) == 0 {
		return false, fmt.Errorf("openai: no moderation results returned")
	}

	return !result.Results[0].Flagged, nil
}

// ── HTTP helpers ──────────────────────────────────────────

func (p *OpenAIProvider) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, p.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai http do: %w", err)
	}
	return resp, nil
}

// ── SSE streaming ────────────────────────────────────────

// toolAcc accumulates tool call data from SSE delta chunks.
type toolAcc struct {
	id        string
	typ       string
	name      string
	arguments strings.Builder
}

func (p *OpenAIProvider) readSSE(ctx context.Context, body io.ReadCloser, events chan<- types.StreamEvent) {
	defer close(events)
	defer func() { _ = body.Close() }()

	reader := bufio.NewReader(body)
	toolAccs := make(map[int]*toolAcc) // keyed by tool call index within the delta array

	send := func(evt types.StreamEvent) bool {
		select {
		case <-ctx.Done():
			return false
		case events <- evt:
			return true
		}
	}

	for {
		select {
		case <-ctx.Done():
			send(types.StreamEvent{Done: true, Error: ctx.Err()})
			return
		default:
		}

		line, err := reader.ReadString('\n')
		if err != nil {
			if err != io.EOF {
				if !send(types.StreamEvent{Done: true, Error: fmt.Errorf("read SSE: %w", err)}) {
					return
				}
			}
			send(types.StreamEvent{Done: true})
			return
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			// Flush any remaining tool accs before signaling done.
			toolCalls := flushToolAccs(toolAccs)
			if len(toolCalls) > 0 {
				if !send(types.StreamEvent{ToolCalls: toolCalls}) {
					return
				}
			}
			send(types.StreamEvent{Done: true})
			return
		}

		var chunk chatCompletionChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			send(types.StreamEvent{Done: true, Error: fmt.Errorf("parse SSE JSON: %w", err)})
			return
		}

		if len(chunk.Choices) > 0 {
			choice := chunk.Choices[0]
			delta := choice.Delta

			// Content delta.
			if delta.Content != "" {
				if !send(types.StreamEvent{Content: delta.Content}) {
					return
				}
			}

			// Accumulate tool call deltas.
			for _, tc := range delta.ToolCalls {
				acc, exists := toolAccs[tc.Index]
				if !exists {
					acc = &toolAcc{}
					toolAccs[tc.Index] = acc
				}
				if tc.ID != "" {
					acc.id = tc.ID
				}
				if tc.Type != "" {
					acc.typ = tc.Type
				}
				if tc.Function.Name != "" {
					acc.name = tc.Function.Name
				}
				acc.arguments.WriteString(tc.Function.Arguments)
			}

			// Finish reason signals the end of the turn.
			if choice.FinishReason == "tool_calls" {
				toolCalls := flushToolAccs(toolAccs)
				if !send(types.StreamEvent{ToolCalls: toolCalls}) {
					return
				}
				send(types.StreamEvent{Done: true})
				return
			}
		}
	}
}

// flushToolAccs converts accumulated tool call data into a slice of ToolCall,
// ordered by index. The accumulator map is cleared after.
func flushToolAccs(accs map[int]*toolAcc) []types.ToolCall {
	if len(accs) == 0 {
		return nil
	}
	// Find max index.
	maxIdx := -1
	for idx := range accs {
		if idx > maxIdx {
			maxIdx = idx
		}
	}
	result := make([]types.ToolCall, 0, maxIdx+1)
	for i := 0; i <= maxIdx; i++ {
		acc, ok := accs[i]
		if !ok {
			continue
		}
		result = append(result, types.ToolCall{
			ID:   acc.id,
			Type: acc.typ,
			Function: types.ToolCallFunction{
				Name:      acc.name,
				Arguments: acc.arguments.String(),
			},
		})
		delete(accs, i)
	}
	return result
}

// ── Request / response types ──────────────────────────────

// openaiTool is the wire format for tool definitions sent to OpenAI.
type openaiTool struct {
	Type     string                  `json:"type"`
	Function tool.FunctionDefinition `json:"function"`
}

// buildOpenAITools converts the provider's tool list into the OpenAI request format.
func (p *OpenAIProvider) buildOpenAITools() []openaiTool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.tools) == 0 {
		return nil
	}
	result := make([]openaiTool, 0, len(p.tools))
	for _, t := range p.tools {
		result = append(result, openaiTool{
			Type: "function",
			Function: tool.FunctionDefinition{
				Name:        t.Name(),
				Description: t.Description(),
				Parameters:  t.Schema(),
			},
		})
	}
	return result
}

func chatRequest(messages []types.Message, config types.ModelConfig, stream bool, tools []openaiTool) types.ChatCompletionRequest {
	req := types.ChatCompletionRequest{
		Model:       config.Model,
		Messages:    messages,
		Temperature: config.Temperature,
		MaxTokens:   config.MaxTokens,
		Stream:      stream,
	}
	if len(tools) > 0 {
		// ChatCompletionRequest.Tools is []any; we marshal by copying.
		req.Tools = make([]any, len(tools))
		for i, t := range tools {
			req.Tools[i] = t
		}
	}
	return req
}

type chatCompletionResponse struct {
	Choices []struct {
		Message struct {
			Role      string             `json:"role"`
			Content   string             `json:"content"`
			ToolCalls []responseToolCall `json:"tool_calls,omitempty"`
		} `json:"message"`
	} `json:"choices"`
}

type responseToolCall struct {
	ID       string               `json:"id"`
	Type     string               `json:"type"`
	Function responseFunctionCall `json:"function"`
}

type responseFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type chatCompletionChunk struct {
	Choices []struct {
		Delta struct {
			Role      string          `json:"role,omitempty"`
			Content   string          `json:"content,omitempty"`
			ToolCalls []chunkToolCall `json:"tool_calls,omitempty"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason,omitempty"`
	} `json:"choices"`
}

// chunkToolCall holds a single tool call delta from an SSE chunk.
type chunkToolCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function"`
}

type embeddingResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

type moderationResponse struct {
	Results []struct {
		Flagged bool `json:"flagged"`
	} `json:"results"`
}

type openAIError struct {
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

func readError(resp *http.Response) error {
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024))
	if err != nil {
		return fmt.Errorf("HTTP %d (failed to read body: %w)", resp.StatusCode, err)
	}

	var apiErr openAIError
	if json.Unmarshal(body, &apiErr) == nil && apiErr.Error.Message != "" {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, apiErr.Error.Message)
	}

	return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
}
