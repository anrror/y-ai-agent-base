package handler

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/anrror/y-ai-agent-base/pkg/agent"
	"github.com/anrror/y-ai-agent-base/pkg/types"
)

// registerRequest is the JSON body for POST /api/v1/agents.
type registerRequest struct {
	AgentID      string                `json:"agent_id"     binding:"required"`
	Model        string                `json:"model"        binding:"required"`
	Identity     *agent.Identity `json:"identity,omitempty"`
	Personality  agent.OCEAN     `json:"personality,omitempty"`
	Temperature  float64               `json:"temperature"`
	MaxTokens    int                   `json:"max_tokens"`
	SystemPrompt string                `json:"system_prompt,omitempty"`
}

// RegisterAgent handles POST /api/v1/agents.
func (h *Handler) RegisterAgent(c *gin.Context) {
	var req registerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	cfg := agent.Config{
		AgentID:     req.AgentID,
		Identity:    req.Identity,
		Personality: req.Personality,
		LLMConfig: types.ModelConfig{
			Model:       req.Model,
			Temperature: req.Temperature,
			MaxTokens:   req.MaxTokens,
		},
		PromptTmpl: req.SystemPrompt,
		Status:     agent.StatusReady,
	}
	cfg.FillDefaults()

	builder := cfg.ToBuilder().
		WithProvider(h.ReadProvider()).
		WithPipeline(h.ReadPipeline())

	ag, err := builder.Build()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.AgentRegistry.Register(ag); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}

	// Persist to store.
	if err := h.AgentStore.Save(c.Request.Context(), "agent:"+req.AgentID, cfg); err != nil {
		slog.ErrorContext(c.Request.Context(), "failed to persist agent config", "agent_id", req.AgentID, "error", err)
	}

	c.JSON(http.StatusCreated, agentResponse(cfg))
}

// ListAgents handles GET /api/v1/agents.
func (h *Handler) ListAgents(c *gin.Context) {
	agents := h.AgentRegistry.List()
	result := make([]gin.H, 0, len(agents))
	for _, ag := range agents {
		result = append(result, agentConfigResponse(ag.GetConfig()))
	}
	c.JSON(http.StatusOK, gin.H{"agents": result, "count": len(result)})
}

// GetAgent handles GET /api/v1/agents/:id.
func (h *Handler) GetAgent(c *gin.Context) {
	id := c.Param("id")
	ag, ok := h.AgentRegistry.Get(id)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "agent not found"})
		return
	}
	c.JSON(http.StatusOK, agentConfigResponse(ag.GetConfig()))
}

// DeleteAgent handles DELETE /api/v1/agents/:id.
func (h *Handler) DeleteAgent(c *gin.Context) {
	id := c.Param("id")
	ag, ok := h.AgentRegistry.Get(id)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "agent not found"})
		return
	}
	if err := h.AgentRegistry.Delete(id); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	if err := ag.Close(); err != nil {
		slog.ErrorContext(c.Request.Context(), "agent close error", "agent_id", id, "error", err)
	}
	if err := h.AgentStore.Delete(c.Request.Context(), "agent:"+id); err != nil {
		slog.ErrorContext(c.Request.Context(), "failed to delete agent config", "agent_id", id, "error", err)
	}
	c.JSON(http.StatusOK, gin.H{"deleted": id})
}

func agentResponse(cfg agent.Config) gin.H {
	return gin.H{
		"agent_id":      cfg.AgentID,
		"model":         cfg.LLMConfig.Model,
		"status":        cfg.Status,
		"identity":      cfg.Identity,
		"personality":   cfg.Personality,
		"temperature":   cfg.LLMConfig.Temperature,
		"max_tokens":    cfg.LLMConfig.MaxTokens,
		"system_prompt": cfg.PromptTmpl,
	}
}

func agentConfigResponse(cfg agent.Config) gin.H {
	resp := agentResponse(cfg)
	resp["safety"] = gin.H{
		"enabled":         cfg.SafetyConfig.Enabled,
		"block_threshold": cfg.SafetyConfig.BlockThreshold,
		"warn_threshold":  cfg.SafetyConfig.WarnThreshold,
	}
	resp["memory"] = gin.H{
		"max_entries":   cfg.MemoryConfig.MaxEntries,
		"ttl_ms":        cfg.MemoryConfig.TTLMillis,
		"consolidation": cfg.MemoryConfig.Consolidation,
	}
	return resp
}
