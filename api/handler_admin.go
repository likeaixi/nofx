package api

import (
	"errors"
	"net/http"

	"nofx/auth"
	"nofx/logger"
	"nofx/store"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// requireOwner gates owner-only admin routes. authMiddleware must have set "role".
func (s *Server) requireOwner() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetString("role") != store.RoleOwner {
			c.JSON(http.StatusForbidden, gin.H{"error": "Owner access required"})
			c.Abort()
			return
		}
		c.Next()
	}
}

func (s *Server) handleCreateInvite(c *gin.Context) {
	code, err := auth.GenerateInviteCode()
	if err != nil {
		SafeInternalError(c, "Failed to generate invite code", err)
		return
	}
	if err := s.store.Invite().Create(code, c.GetString("user_id")); err != nil {
		SafeInternalError(c, "Failed to store invite code", err)
		return
	}
	logger.Infof("✉️  Invite %s created by owner %s", code, c.GetString("email"))
	c.JSON(http.StatusOK, gin.H{"code": code})
}

func (s *Server) handleListInvites(c *gin.Context) {
	invites, err := s.store.Invite().List()
	if err != nil {
		SafeInternalError(c, "Failed to list invites", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"invites": invites})
}

func (s *Server) handleRevokeInvite(c *gin.Context) {
	if err := s.store.Invite().Revoke(c.Param("code")); errors.Is(err, store.ErrInviteUnavailable) {
		c.JSON(http.StatusNotFound, gin.H{"error": "Invite not found or already used"})
		return
	} else if err != nil {
		SafeInternalError(c, "Failed to revoke invite", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Invite revoked"})
}

func (s *Server) handleAdminListUsers(c *gin.Context) {
	users, err := s.store.User().GetAll()
	if err != nil {
		SafeInternalError(c, "Failed to list users", err)
		return
	}
	out := make([]gin.H, 0, len(users))
	for _, u := range users {
		out = append(out, gin.H{
			"id": u.ID, "email": u.Email, "role": u.Role,
			"disabled": u.Disabled, "created_at": u.CreatedAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{"users": out})
}

func (s *Server) handleAdminSetUserDisabled(c *gin.Context) {
	targetID := c.Param("id")
	if targetID == c.GetString("user_id") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "You cannot disable your own account"})
		return
	}
	var req struct {
		Disabled *bool `json:"disabled" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		SafeBadRequest(c, "disabled (bool) is required")
		return
	}
	err := s.store.User().SetDisabled(targetID, *req.Disabled)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}
	if err != nil {
		SafeInternalError(c, "Failed to update user", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "User updated"})
}
