package admin

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *AccountHandler) GetOpenAIEvalConfig(c *gin.Context) {
	if h.openAIEvalService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "OpenAI evaluation service is unavailable"})
		return
	}
	config, err := h.openAIEvalService.GetConfig(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load evaluation config"})
		return
	}
	c.JSON(http.StatusOK, config)
}

func (h *AccountHandler) UpdateOpenAIEvalConfig(c *gin.Context) {
	if h.openAIEvalService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "OpenAI evaluation service is unavailable"})
		return
	}
	var config service.OpenAIEvalConfig
	if err := c.ShouldBindJSON(&config); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid evaluation config"})
		return
	}
	actorID, _ := c.Request.Context().Value(ctxkey.UserID).(int64)
	if err := h.openAIEvalService.SaveConfig(c.Request.Context(), &config, actorID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// SaveConfig strips runtime fields before persistence. Return a fresh
	// projection so BPS state and current account eligibility remain visible
	// immediately after saving.
	saved, err := h.openAIEvalService.GetConfig(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to reload evaluation config"})
		return
	}
	c.JSON(http.StatusOK, saved)
}

func (h *AccountHandler) RunOpenAIEval(c *gin.Context) {
	if h.openAIEvalService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "OpenAI evaluation service is unavailable"})
		return
	}
	var request service.OpenAIEvalRunRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid evaluation request"})
		return
	}
	actorID, _ := c.Request.Context().Value(ctxkey.UserID).(int64)
	run, err := h.openAIEvalService.Run(c.Request.Context(), request, actorID, "manual")
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "already running") {
			status = http.StatusConflict
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, run)
}

func (h *AccountHandler) ListOpenAIEvalRuns(c *gin.Context) {
	if h.openAIEvalService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "OpenAI evaluation service is unavailable"})
		return
	}
	filter := service.OpenAIEvalRunFilter{
		RequestedModel:  strings.TrimSpace(c.Query("requested_model")),
		ReasoningEffort: strings.TrimSpace(c.Query("reasoning_effort")),
		TestType:        strings.TrimSpace(c.Query("test_type")),
		Limit:           100,
	}
	if raw := strings.TrimSpace(c.Query("account_id")); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id < 1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid account_id"})
			return
		}
		filter.AccountID = id
	}
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid limit"})
			return
		}
		filter.Limit = limit
	}
	items, err := h.openAIEvalService.ListRuns(c.Request.Context(), filter)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load evaluation history"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (h *AccountHandler) ListOpenAIEvalAudit(c *gin.Context) {
	if h.openAIEvalService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "OpenAI evaluation service is unavailable"})
		return
	}
	items, err := h.openAIEvalService.ListAuditEvents(c.Request.Context(), 100)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load evaluation audit"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (h *AccountHandler) ListOpenAIEvalModels(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"items":            service.OpenAIEvalSupportedModels(),
		"baseline_version": service.OpenAIEvalBaselineVersion,
		"baseline_models": func() []string {
			out := make([]string, 0, len(service.OpenAIEvalFingerprintBaselines))
			for _, item := range service.OpenAIEvalFingerprintBaselines {
				out = append(out, item.Model)
			}
			return out
		}(),
		"candy":             gin.H{"expected_answer": service.OpenAIEvalCandyExpectedAnswer, "confidence": "low", "scheduling": "alert_only"},
		"modeltrace":        gin.H{"requests": service.OpenAIEvalModelTraceRequests, "bank_revision": "df3a0f9d3e054c0dc02d6d586686db8daf8fa7c8", "candidate_count": 16, "scheduling": "alert_only"},
		"evaluation_notice": "Fingerprint and ModelTrace are behavioral attribution evidence, not intelligence verdicts. Candy is a low-confidence public canary. Insufficient or uncertain results never change scheduling.",
		"reasoning_efforts": []string{"", "minimal", "low", "medium", "high", "xhigh", "max"},
		"fingerprint_modes": []gin.H{
			{"id": "quick", "samples": service.OpenAIEvalFingerprintQuickSamples},
			{"id": "standard", "samples": service.OpenAIEvalFingerprintStandardSamples},
			{"id": "strict", "samples": service.OpenAIEvalFingerprintStrictSamples},
		},
	})
}
