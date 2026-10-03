package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/service/basispoints"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

var openAIBPSReplay basispoints.ReplayCache
var openAIBPSCatalog basispoints.CatalogCache

// Returned only before any client response is committed. The caller can then
// continue through the native Responses path.
var errOpenAIBPSNativeFallback = errors.New("BPS request requires native Responses fallback")

func (s *OpenAIGatewayService) forwardOpenAIBPS(ctx context.Context, c *gin.Context, account *Account, body []byte, start time.Time) (*OpenAIForwardResult, error) {
	requestedModel := strings.TrimSpace(gjson.GetBytes(body, "model").String())
	if !s.isOpenAIBPSForwardEligible(ctx, account, requestedModel) {
		return nil, errOpenAIBPSNativeFallback
	}
	if !gjson.GetBytes(body, "stream").Bool() || isOpenAIResponsesCompactPath(c) {
		return nil, errOpenAIBPSNativeFallback
	}
	// Keep BPS model resolution identical to State Probe/evaluation target
	// resolution so a configured alias cannot silently probe one model and
	// forward another.
	model := normalizeOpenAIModelForUpstream(account, account.GetMappedModel(requestedModel))
	if normalized, changed, err := normalizeOpenAIPassthroughOAuthBody(body, false); err != nil {
		return nil, errOpenAIBPSNativeFallback
	} else if changed {
		body = normalized
	}
	if model != requestedModel {
		mappedBody, mapErr := sjson.SetBytes(body, "model", model)
		if mapErr != nil {
			return nil, errOpenAIBPSNativeFallback
		}
		body = mappedBody
	}
	scope := strconv.FormatInt(account.ID, 10) + ":" + strconv.FormatInt(getAPIKeyIDFromContext(c), 10) + ":" + s.GenerateSessionHash(c, body)
	upstreamBody, bridge, err := basispoints.PrepareWithCatalog(body, scope, &openAIBPSReplay, &openAIBPSCatalog)
	if err != nil {
		return nil, errOpenAIBPSNativeFallback
	}
	token, _, err := s.GetAccessToken(ctx, account)
	if err != nil || strings.TrimSpace(token) == "" {
		return nil, errOpenAIBPSNativeFallback
	}
	accountID := strings.TrimSpace(account.GetChatGPTAccountID())
	if accountID == "" {
		claims, decodeErr := openai.DecodeIDToken(token)
		if decodeErr == nil && claims.OpenAIAuth != nil {
			accountID = strings.TrimSpace(claims.OpenAIAuth.ChatGPTAccountID)
		}
	}
	if accountID == "" {
		return nil, errOpenAIBPSNativeFallback
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, basispoints.ResponsesURL, bytes.NewReader(upstreamBody))
	if err != nil {
		return nil, errOpenAIBPSNativeFallback
	}
	req = req.WithContext(WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileOpenAI))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Chatgpt-Account-Id", accountID)
	req.Header.Set("X-Openai-Account-Id", accountID)
	req.Header.Set("X-Basispoints-Auth-Mode", "chatgpt")
	req.Header.Set("X-Openai-Internal-Basispoints-Client-Product", "basispoints-excel-plugin")
	req.Header.Set("X-Openai-Internal-Basispoints-Client-Agent-Profile", "excel")
	req.Header.Set("Origin", "https://bps.openai.com")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	// BPS is still a Codex OAuth outbound route. Keep the same fixed locale
	// identity as native Responses so the host/client locale never leaks.
	enforceCodexAcceptLanguage(req.Header)
	proxy := ""
	if account.Proxy != nil {
		proxy = account.Proxy.URL()
	}
	SetActualOpenAIUpstreamEndpoint(c, "/basispoints/api/responses")
	SetOpsUpstreamModel(c, model)
	resp, err := s.doAccountRPMUpstream(req, proxy, account)
	if IsAccountRPMError(err) {
		return nil, err
	}
	if err != nil {
		// The bridge has not committed anything downstream yet. Let the native
		// Responses path make the same request instead of exposing a bridge-only
		// transport failure to the client.
		return nil, errOpenAIBPSNativeFallback
	}
	if resp == nil || resp.Body == nil {
		return nil, errOpenAIBPSNativeFallback
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		if resp.StatusCode == http.StatusForbidden {
			s.disableOpenAIBPSAfter403(ctx, account)
		}
		return nil, errOpenAIBPSNativeFallback
	}
	converted := bridge.StreamWithRepairs(ctx, resp.Body, nil, nil)
	defer func() { _ = converted.Close() }()
	convertedResp := *resp
	convertedResp.Body = converted
	convertedResp.Header = resp.Header.Clone()
	convertedResp.Header.Set("Content-Type", "text/event-stream")
	effort := bridge.Effort
	streamResult, err := s.handleStreamingResponseWithReasoning(ctx, &convertedResp, c, account, start, requestedModel, model, effort)
	if err != nil {
		if streamResult == nil || (!streamResult.responsesMeaningfulOutput && !streamResult.responsesToolCallForwarded && !IsResponseCommitted(c)) {
			return nil, errOpenAIBPSNativeFallback
		}
		result := openAIBPSForwardResult(resp, streamResult, requestedModel, model, effort, start)
		if streamResult.responseID != "" && (streamResult.responsesMeaningfulOutput || streamResult.responsesToolCallForwarded) {
			s.bindHTTPResponseAccount(ctx, c, account, streamResult.responseID)
		}
		return result, err
	}
	s.bindHTTPResponseAccount(ctx, c, account, streamResult.responseID)
	return openAIBPSForwardResult(resp, streamResult, requestedModel, model, effort, start), nil
}

func openAIBPSForwardResult(resp *http.Response, streamResult *openaiStreamingResult, requestedModel, upstreamModel, effort string, start time.Time) *OpenAIForwardResult {
	var usage OpenAIUsage
	if streamResult != nil && streamResult.usage != nil {
		usage = *streamResult.usage
	}
	result := &OpenAIForwardResult{RequestID: resp.Header.Get("x-request-id"), Model: requestedModel, UpstreamModel: upstreamModel, UpstreamEndpoint: "/basispoints/api/responses", ReasoningEffort: &effort, Stream: true, Duration: time.Since(start), Usage: usage}
	if streamResult == nil {
		return result
	}
	result.ResponseID = streamResult.responseID
	result.UpstreamHeaders = resp.Header
	result.FirstTokenMs = streamResult.firstTokenMs
	result.ResponsesOutcomeObserved = streamResult.responsesOutcomeObserved
	result.ResponsesProtocolStatus = streamResult.responsesProtocolStatus
	result.ResponsesStatus = streamResult.responsesStatus
	result.ResponsesIncompleteReason = streamResult.responsesIncompleteReason
	result.ResponsesMeaningfulOutput = streamResult.responsesMeaningfulOutput
	result.ResponsesToolCallForwarded = streamResult.responsesToolCallForwarded
	result.UpstreamTerminalEvent = streamResult.responsesTerminalEvent
	result.ToolCapabilityFailure = streamResult.toolCapabilityFailure
	result.ExecCallObserved = streamResult.execCallObserved
	return result
}

func openAIBPSReject(c *gin.Context, status int, code, message string) error {
	if c != nil {
		MarkResponseCommitted(c)
		c.JSON(status, gin.H{"error": gin.H{"type": "server_error", "code": code, "message": message}})
	}
	return fmt.Errorf("BPS %s", code)
}

func (s *OpenAIGatewayService) disableOpenAIBPSAfter403(ctx context.Context, account *Account) {
	if s == nil || s.accountRepo == nil || account == nil {
		return
	}
	stateCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := updateOpenAIBPSAccountState(stateCtx, s.accountRepo, account.ID, func(state OpenAIBPSAccountState) (OpenAIBPSAccountState, bool) {
		state.Active = false
		state.DisabledReason = "upstream_403"
		state.UpdatedAt = time.Now().UTC()
		return state, true
	}); err != nil {
		logger.LegacyPrintf("service.openai_eval", "[OpenAI BPS] failed to persist 403 disable account=%d: %v", account.ID, err)
	}
}
