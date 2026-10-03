package admin

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type openAIEvalConfigWithQualityStatus struct {
	*service.OpenAIEvalConfig
	QualityRefreshedAt   *time.Time `json:"quality_refreshed_at"`
	QualityNextRefreshAt *time.Time `json:"quality_next_refresh_at"`
}

func openAIEvalConfigQualityStatus(config *service.OpenAIEvalConfig) openAIEvalConfigWithQualityStatus {
	response := openAIEvalConfigWithQualityStatus{OpenAIEvalConfig: config}
	status := service.OpenAIEvalQualityRefreshStatus()
	if !status.RefreshedAt.IsZero() {
		response.QualityRefreshedAt = &status.RefreshedAt
		response.QualityNextRefreshAt = &status.NextRefreshAt
	}
	return response
}

func (h *AccountHandler) RefreshOpenAIEvalQuality(c *gin.Context) {
	if h.openAIEvalService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "OpenAI evaluation service is unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	actorID, _ := ctx.Value(ctxkey.UserID).(int64)
	result, err := h.openAIEvalService.RefreshOpenAIEvalQuality(ctx, actorID)
	if err != nil {
		if errors.Is(err, service.ErrOpenAIEvalQualityRefreshSuperseded) {
			c.JSON(http.StatusConflict, gin.H{"error": "quality configuration changed or a newer refresh started; retry refresh"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to refresh evaluation quality"})
		}
		return
	}
	c.JSON(http.StatusOK, result)
}
