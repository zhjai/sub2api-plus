package admin

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func openAIEvalRankingHTTPError(c *gin.Context, err error) {
	status, code, message := http.StatusInternalServerError, "EVALUATION_FAILED", "Scheduling evaluation failed"
	switch {
	case errors.Is(err, service.ErrOpenAIEvalRankingUnavailable):
		status, code, message = http.StatusServiceUnavailable, "EVALUATION_SERVICE_UNAVAILABLE", "Scheduling evaluation service is unavailable"
	case errors.Is(err, service.ErrOpenAIEvalRankingSuperseded):
		status, code, message = http.StatusConflict, "EVALUATION_SUPERSEDED", "A newer saved configuration superseded this evaluation"
	case errors.Is(err, service.ErrOpenAIEvalRankingSnapshotChanged):
		status, code, message = http.StatusConflict, "RANKING_SNAPSHOT_CHANGED", "The requested ranking generation is no longer retained"
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		status, code, message = http.StatusGatewayTimeout, "EVALUATION_TIMEOUT", "Scheduling evaluation did not complete within its budget"
	}
	c.JSON(status, gin.H{"error": message, "code": code})
}

func (h *AccountHandler) EvaluateOpenAIScheduling(c *gin.Context) {
	if h.openAIEvalService == nil {
		openAIEvalRankingHTTPError(c, service.ErrOpenAIEvalRankingUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	actor, _ := ctx.Value(ctxkey.UserID).(int64)
	summary, err := h.openAIEvalService.EvaluateScheduling(ctx, actor)
	if err != nil {
		openAIEvalRankingHTTPError(c, err)
		return
	}
	c.JSON(http.StatusOK, summary)
}

func (h *AccountHandler) GetOpenAISchedulingRankings(c *gin.Context) {
	h.getOpenAISchedulingRanking(c, false)
}

func (h *AccountHandler) GetOpenAISchedulingAccountOverview(c *gin.Context) {
	h.getOpenAISchedulingRanking(c, true)
}

func (h *AccountHandler) getOpenAISchedulingRanking(c *gin.Context, overview bool) {
	if h.openAIEvalService == nil {
		openAIEvalRankingHTTPError(c, service.ErrOpenAIEvalRankingUnavailable)
		return
	}
	filter := service.OpenAIEvalRankingFilter{EvaluationID: c.Query("evaluation_id"), Cursor: c.Query("cursor"), Limit: 100}
	if value, present := c.GetQuery("group_id"); present {
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil || id < 0 {
			c.JSON(400, gin.H{"error": "invalid group_id"})
			return
		}
		filter.GroupID = &id
	}
	if values, present := c.Request.URL.Query()["requested_model"]; present {
		value := values[0]
		filter.RequestedModel = &value
	}
	if values, present := c.Request.URL.Query()["reasoning_effort"]; present {
		value := values[0]
		filter.ReasoningEffort = &value
	}
	if value, present := c.GetQuery("limit"); present {
		limit, err := strconv.Atoi(value)
		if err != nil || limit < 1 || limit > 500 {
			c.JSON(400, gin.H{"error": "limit must be between 1 and 500"})
			return
		}
		filter.Limit = limit
	}
	var result any
	var err error
	if overview {
		result, err = h.openAIEvalService.SchedulingAccountOverview(filter)
	} else {
		result, err = h.openAIEvalService.SchedulingRankings(filter)
	}
	if err != nil {
		if errors.Is(err, service.ErrOpenAIEvalRankingUnavailable) || errors.Is(err, service.ErrOpenAIEvalRankingSnapshotChanged) {
			openAIEvalRankingHTTPError(c, err)
		} else {
			c.JSON(400, gin.H{"error": "invalid ranking filter or cursor"})
		}
		return
	}
	c.JSON(http.StatusOK, result)
}
