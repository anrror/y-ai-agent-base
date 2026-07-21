package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// teamInfo is the JSON response for a team listing.
type teamInfo struct {
	ID         string   `json:"id"`
	Supervisor string   `json:"supervisor"`
	Members    []string `json:"members"`
	AgentCount int      `json:"agent_count"`
}

// ListTeams handles GET /api/v1/teams.
func (h *Handler) ListTeams(c *gin.Context) {
	teams := h.TeamRegistry.List()
	infos := make([]teamInfo, 0, len(teams))
	for _, t := range teams {
		infos = append(infos, teamInfo{
			ID:         t.ID,
			Supervisor: t.Supervisor.ID(),
			Members:    t.AgentIDs(),
			AgentCount: len(t.Members),
		})
	}
	c.JSON(http.StatusOK, gin.H{"teams": infos})
}

// GetTeam handles GET /api/v1/teams/:id.
func (h *Handler) GetTeam(c *gin.Context) {
	id := c.Param("id")
	t, ok := h.TeamRegistry.Get(id)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "team not found: " + id})
		return
	}
	c.JSON(http.StatusOK, teamInfo{
		ID:         t.ID,
		Supervisor: t.Supervisor.ID(),
		Members:    t.AgentIDs(),
		AgentCount: len(t.Members),
	})
}
