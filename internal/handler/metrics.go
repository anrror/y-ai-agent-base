package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Metrics handles GET /metrics.
// Returns a JSON snapshot of pipeline request metrics.
func (h *Handler) Metrics(c *gin.Context) {
	if h.PipelineMetrics == nil {
		c.JSON(http.StatusOK, gin.H{
			"total":   int64(0),
			"success": int64(0),
			"failed":  int64(0),
		})
		return
	}

	snap := h.PipelineMetrics.Snapshot()
	status := http.StatusOK
	c.JSON(status, snap)
}
