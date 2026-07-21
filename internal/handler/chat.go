package handler

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/anrror/y-ai-agent-base/pkg/agent"
	"github.com/anrror/y-ai-agent-base/pkg/types"
)

// genChatID generates a random chat completion ID using crypto/rand.
func genChatID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// Fallback: timestamp-based ID if crypto/rand fails.
		return fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
	}
	return "chatcmpl-" + hex.EncodeToString(b)
}

// ChatCompletions handles POST /api/v1/chat/completions.
// Accepts an OpenAI-compatible chat completion request and streams SSE
// events when stream=true. Non-streaming requests return the full response
// as JSON.
//
// The model field supports:
//   - "<agent_id>" — direct agent chat
//   - "team:<team_id>" — team supervisor chat (routes to team supervisor agent)
func (h *Handler) ChatCompletions(c *gin.Context) {
	var req types.ChatCompletionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid request: %v", err)})
		return
	}

	if req.Model == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "model is required"})
		return
	}

	if len(req.Messages) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "messages array must not be empty"})
		return
	}

	// Resolve target agent: support "team:<id>" prefix for team supervisors.
	ag, err := h.resolveAgent(req.Model)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	c.Set("agent_id", ag.ID())

	// Use the agent's configured model for the provider, not req.Model.
	agCfg := ag.GetConfig()
	modelCfg := types.ModelConfig{
		Model:       agCfg.LLMConfig.Model,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
	}

	input := types.ChatInput{
		Messages:    req.Messages,
		ModelConfig: &modelCfg,
	}

	if req.Stream {
		h.handleStream(c, ag, input)
		return
	}

	h.handleJSON(c, ag, input)
}

func (h *Handler) handleJSON(c *gin.Context, ag *agent.Agent, input types.ChatInput) {
	output, err := ag.Run(c.Request.Context(), input)
	if err != nil {
		slog.ErrorContext(
			c.Request.Context(), "agent run failed",
			"agent_id", ag.ID(),
			"error", err,
		)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"id":      genChatID(),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   ag.ID(),
		"choices": []gin.H{
			{
				"index":         0,
				"message":       gin.H{"role": output.Role, "content": output.Content},
				"finish_reason": "stop",
			},
		},
		// TODO: Extract actual token usage from provider response.
		// Currently hardcoded to 0; requires Run() to return usage metadata.
		"usage": gin.H{
			"prompt_tokens":     0,
			"completion_tokens": 0,
			"total_tokens":      0,
		},
	})
}

type sseChunk struct {
	ID      string      `json:"id"`
	Object  string      `json:"object"`
	Created int64       `json:"created"`
	Model   string      `json:"model"`
	Choices []sseChoice `json:"choices"`
}

type sseChoice struct {
	Index        int      `json:"index"`
	Delta        sseDelta `json:"delta"`
	FinishReason *string  `json:"finish_reason,omitempty"`
}

type sseDelta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

func (h *Handler) handleStream(c *gin.Context, ag *agent.Agent, input types.ChatInput) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)

	events := make(chan types.StreamEvent, 32)
	id := genChatID()
	model := ag.ID()
	created := time.Now().Unix()

	flusher, canFlush := c.Writer.(http.Flusher)
	send := func(data sseChunk) {
		b, err := json.Marshal(data)
		if err != nil {
			slog.ErrorContext(c.Request.Context(), "sse marshal error", "error", err)
			return
		}
		if _, err := fmt.Fprintf(c.Writer, "data: %s\n\n", b); err != nil {
			slog.ErrorContext(c.Request.Context(), "sse write error", "error", err)
		}
		if canFlush {
			flusher.Flush()
		}
	}

	send(sseChunk{
		ID:      id,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   model,
		Choices: []sseChoice{{Index: 0, Delta: sseDelta{Role: "assistant"}}},
	})

	go func() {
		defer close(events)
		if err := ag.RunStream(c.Request.Context(), input, events); err != nil {
			select {
			case events <- types.StreamEvent{Done: true, Error: err}:
			case <-c.Request.Context().Done():
			}
		}
	}()

	finishReason := "stop"
	for evt := range events {
		if evt.Error != nil {
			slog.ErrorContext(
				c.Request.Context(), "stream error",
				"agent_id", model, "error", evt.Error,
			)
			break
		}
		if evt.Done {
			break
		}
		if evt.Content != "" {
			send(sseChunk{
				ID:      id,
				Object:  "chat.completion.chunk",
				Created: created,
				Model:   model,
				Choices: []sseChoice{{Index: 0, Delta: sseDelta{Content: evt.Content}}},
			})
		}
	}

	send(sseChunk{
		ID:      id,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   model,
		Choices: []sseChoice{{Index: 0, FinishReason: &finishReason}},
	})

	_, _ = fmt.Fprint(c.Writer, "data: [DONE]\n\n")
	if canFlush {
		flusher.Flush()
	}
}

// resolveAgent resolves an agent by model string. Supports two formats:
//   - "<agent_id>" — looks up the agent directly
//   - "team:<team_id>" — resolves to the team's supervisor agent
func (h *Handler) resolveAgent(model string) (*agent.Agent, error) {
	// Check for team: prefix.
	const teamPrefix = "team:"
	if len(model) >= len(teamPrefix) && model[:len(teamPrefix)] == teamPrefix {
		teamID := model[len(teamPrefix):]
		t, ok := h.TeamRegistry.Get(teamID)
		if !ok {
			return nil, fmt.Errorf("team %q not found", teamID)
		}
		return t.Supervisor, nil
	}

	// Direct agent lookup.
	ag, ok := h.AgentRegistry.Get(model)
	if !ok {
		// Fallback: scan agents by their configured LLM model.
		for _, candidate := range h.AgentRegistry.List() {
			if candidate.GetConfig().LLMConfig.Model == model {
				return candidate, nil
			}
		}
		return nil, fmt.Errorf("agent for model %q not found", model)
	}
	return ag, nil
}
