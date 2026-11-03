package admin

import (
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *AccountHandler) CreatePelicanFrameToken(c *gin.Context) {
	if h.openAIEvalService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "evaluation service unavailable"})
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	var request struct {
		Mode string `json:"mode"`
	}
	if err != nil || id <= 0 || c.ShouldBindJSON(&request) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid Pelican preview request"})
		return
	}
	token, expires, err := h.openAIEvalService.CreatePelicanFrameToken(c.Request.Context(), id, request.Mode)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"url": "/api/v1/openai-eval/pelican-frame/" + token, "expires_at": expires})
}

func (h *AccountHandler) ServePelicanFrame(c *gin.Context) {
	c.Header("Content-Security-Policy", service.PelicanFrameCSP)
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Referrer-Policy", "no-referrer")
	c.Header("Cache-Control", "private, no-store")
	c.Header("X-Frame-Options", "SAMEORIGIN")
	if h.openAIEvalService == nil {
		c.Status(http.StatusServiceUnavailable)
		return
	}
	html, err := h.openAIEvalService.ResolvePelicanFrame(c.Request.Context(), c.Param("token"))
	if err != nil {
		c.Status(http.StatusForbidden)
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(html))
}
