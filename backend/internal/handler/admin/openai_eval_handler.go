package admin

import (
	"encoding/json"
	"errors"
	"io"
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
	payload, err := io.ReadAll(io.LimitReader(c.Request.Body, 4<<20))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid evaluation config"})
		return
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil || fields == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid evaluation config"})
		return
	}
	var config service.OpenAIEvalConfig
	if err := json.Unmarshal(payload, &config); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid evaluation config"})
		return
	}
	// Whole-object saves from older admin clients predate revision, policy, and
	// explicit BPS mode fields. Merge only omitted fields from the current
	// projection so an old client cannot silently erase newer controls. Explicit
	// false, empty arrays, or an empty BPS mode remain valid clear operations.
	current, err := h.openAIEvalService.GetConfig(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load current evaluation config"})
		return
	}
	mergeOpenAIEvalConfigOmittedFields(&config, current, fields)
	actorID, _ := c.Request.Context().Value(ctxkey.UserID).(int64)
	if err := h.openAIEvalService.SaveConfig(c.Request.Context(), &config, actorID); err != nil {
		if errors.Is(err, service.ErrOpenAIEvalConfigRevisionConflict) {
			c.JSON(http.StatusConflict, gin.H{"error": "evaluation config changed; reload before saving"})
			return
		}
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

func mergeOpenAIEvalConfigOmittedFields(incoming, current *service.OpenAIEvalConfig, fields map[string]json.RawMessage) {
	if incoming == nil || current == nil {
		return
	}
	if _, ok := fields["revision"]; !ok {
		incoming.Revision = current.Revision
	}
	if _, ok := fields["effects_enabled"]; !ok {
		incoming.EffectsEnabled = current.EffectsEnabled
	}
	if _, ok := fields["bps_auto_enabled"]; !ok {
		incoming.BPSAutoEnabled = current.BPSAutoEnabled
	}
	if _, ok := fields["scheduling_policy"]; !ok {
		incoming.SchedulingPolicy = current.SchedulingPolicy
	}
	if _, ok := fields["policies"]; !ok {
		incoming.Policies = current.Policies
	}
	if _, ok := fields["custom_balance"]; !ok {
		incoming.CustomBalance = current.CustomBalance
	}
	if _, ok := fields["bps_accounts"]; !ok {
		incoming.BPSAccounts = current.BPSAccounts
	}
	if raw, ok := fields["accounts"]; !ok {
		incoming.Accounts = current.Accounts
	} else {
		var rawRoutes []map[string]json.RawMessage
		if json.Unmarshal(raw, &rawRoutes) == nil {
			byKey := make(map[string]service.OpenAIEvalAccountConfig, len(current.Accounts))
			for _, route := range current.Accounts {
				byKey[openAIEvalRouteConfigKey(route)] = route
			}
			for i := range incoming.Accounts {
				if previous, exists := byKey[openAIEvalRouteConfigKey(incoming.Accounts[i])]; exists {
					mergeOpenAIEvalRouteBPSFields(&incoming.Accounts[i], previous, rawRoutesField(rawRoutes, i))
				}
			}
		}
	}
}

func mergeOpenAIEvalRouteBPSFields(incoming *service.OpenAIEvalAccountConfig, previous service.OpenAIEvalAccountConfig, fields map[string]json.RawMessage) {
	if incoming == nil {
		return
	}
	if _, modePresent := fields["bps_mode"]; modePresent {
		// An explicit mode, including an empty string, is a deliberate write.
		// The service normalizer turns an empty mode into force_off.
		return
	}
	if rawAuto, autoPresent := fields["bps_auto"]; autoPresent {
		var legacyAuto bool
		if err := json.Unmarshal(rawAuto, &legacyAuto); err == nil {
			incoming.BPSAuto = legacyAuto
			if legacyAuto {
				incoming.BPSMode = service.OpenAIEvalBPSModeAuto
			} else {
				incoming.BPSMode = service.OpenAIEvalBPSModeForceOff
			}
		}
		return
	}
	// Neither field was sent by the old client. Preserve both stored values.
	incoming.BPSMode = previous.BPSMode
	incoming.BPSAuto = previous.BPSAuto
}

func rawRoutesField(routes []map[string]json.RawMessage, index int) map[string]json.RawMessage {
	if index < 0 || index >= len(routes) || routes[index] == nil {
		return map[string]json.RawMessage{}
	}
	return routes[index]
}

func openAIEvalRouteConfigKey(route service.OpenAIEvalAccountConfig) string {
	return strconv.FormatInt(route.AccountID, 10) + "\x00" + strings.ToLower(strings.TrimSpace(route.RequestedModel)) + "\x00" + strings.ToLower(strings.TrimSpace(route.ReasoningEffort))
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

func (h *AccountHandler) ResetOpenAIEvalBPSState(c *gin.Context) {
	if h.openAIEvalService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "OpenAI evaluation service is unavailable"})
		return
	}
	var request struct {
		AccountID      int64  `json:"account_id"`
		RequestedModel string `json:"requested_model"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid BPS reset request"})
		return
	}
	actorID, _ := c.Request.Context().Value(ctxkey.UserID).(int64)
	state, err := h.openAIEvalService.ResetOpenAIBPSState(c.Request.Context(), request.AccountID, request.RequestedModel, actorID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"state": state})
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
