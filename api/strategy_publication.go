package api

import (
	"github.com/gin-gonic/gin"
	"net/http"
)

func (s *Server) handleStrategyPublication(c *gin.Context) {
	user, id := c.GetString("user_id"), c.Param("id")
	if user == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
		return
	}
	var req struct {
		IsPublic      *bool `json:"is_public"`
		ConfigVisible *bool `json:"config_visible"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.IsPublic == nil || req.ConfigVisible == nil || (*req.ConfigVisible && !*req.IsPublic) {
		SafeBadRequest(c, "Provide publication and parameter visibility; private strategies cannot expose parameters")
		return
	}
	unlock, err := s.lockStrategyTraders(user, id)
	if err != nil {
		SafeInternalError(c, "Lock strategy", err)
		return
	}
	defer unlock()
	if err := s.store.Strategy().UpdatePublication(user, id, *req.IsPublic, *req.ConfigVisible); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Strategy not found or is a read-only system template"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"is_public": *req.IsPublic, "config_visible": *req.ConfigVisible})
}
