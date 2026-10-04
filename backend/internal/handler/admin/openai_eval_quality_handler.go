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
	QualityRefreshedAt   *time.Time                        `json:"quality_refreshed_at"`
	QualityNextRefreshAt *time.Time                        `json:"quality_next_refresh_at"`
	Ranking              *service.OpenAIEvalRankingSummary `json:"ranking"`
	RankingError         *service.OpenAIEvalRankingError   `json:"ranking_error"`
	EffectiveStatus      string                            `json:"effective_status"`
	EvaluationInProgress bool                              `json:"evaluation_in_progress"`
	SavedRevision        *int64                            `json:"saved_revision,omitempty"`
}

func (h *AccountHandler) openAIEvalConfigRankingStatus(config *service.OpenAIEvalConfig, saved bool) openAIEvalConfigWithQualityStatus {
	response := openAIEvalConfigQualityStatus(config)
	response.EffectiveStatus = "inactive_effects_off"
	if snapshot, err := h.openAIEvalService.SchedulingRankings(service.OpenAIEvalRankingFilter{Limit: 1}); err == nil {
		response.Ranking, response.RankingError = snapshot.Summary, snapshot.RankingError
		response.EffectiveStatus, response.EvaluationInProgress = snapshot.EffectiveStatus, snapshot.EvaluationInProgress
	}
	if saved && config != nil {
		revision := config.Revision
		response.SavedRevision = &revision
	}
	return response
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
		if errors.Is(err, service.ErrOpenAIEvalRankingSuperseded) || errors.Is(err, service.ErrOpenAIEvalRankingUnavailable) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			openAIEvalRankingHTTPError(c, err)
			return
		}
		if errors.Is(err, service.ErrOpenAIEvalQualityRefreshSuperseded) {
			c.JSON(http.StatusConflict, gin.H{"error": "quality configuration changed or a newer refresh started; retry refresh"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to refresh evaluation quality"})
		}
		return
	}
	c.JSON(http.StatusOK, result)
}
