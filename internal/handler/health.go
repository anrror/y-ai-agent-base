package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

const statusDegraded = "degraded"

// Health handles GET /health.
// Returns a basic health status including a DB ping check and provider check.
func (h *Handler) Health(c *gin.Context) {
	status := "ok"
	dbStatus := "ok"
	providerStatus := "ok"

	// Check agent store health with a brief timeout.
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()

	// For MemoryStore, LoadAll serves as a connectivity check.
	if _, err := h.AgentStore.LoadAll(ctx); err != nil {
		dbStatus = statusDegraded
	}

	// Ping the LLM provider with a separate timeout.
	pingCtx, pingCancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer pingCancel()

	if err := h.ReadProvider().Ping(pingCtx); err != nil {
		providerStatus = statusDegraded
	}

	if dbStatus != "ok" || providerStatus != "ok" {
		status = statusDegraded
	}

	c.JSON(http.StatusOK, gin.H{
		"status":    status,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"database":  dbStatus,
		"provider":  providerStatus,
	})
}
