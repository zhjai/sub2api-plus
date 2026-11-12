package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service/basispoints"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

var excelBPSReplay basispoints.ReplayCache
var excelBPSCatalog basispoints.CatalogCache

func redactExcelBPSCredentialBody(raw []byte, account *Account, accessToken string) []byte {
	for _, secret := range []string{accessToken, account.GetCredential("access_token"), account.GetCredential("refresh_token"), account.GetCredential("id_token")} {
		if secret != "" {
			raw = bytes.ReplaceAll(raw, []byte(secret), []byte("[REDACTED]"))
		}
	}
	return raw
}

func excelBPSUnsupported(c *gin.Context) error {
	err := infraerrors.New(http.StatusBadRequest, "EXCEL_OAUTH_ROUTE_UNAVAILABLE", "BPS OAuth supports HTTP Responses only; native Codex, WebSocket, compact and image endpoints require a separate Codex account")
	if c != nil && !c.Writer.Written() {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"type": "invalid_request_error", "code": "excel_oauth_route_unavailable", "message": err.Error()}})
	}
	return err
}

func newExcelBPSRequest(ctx context.Context, body []byte, token, accountID string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(WithHTTPUpstreamRedirectsDisabled(ctx), http.MethodPost, basispoints.ResponsesURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	// Construct headers from scratch. Never carry Codex installation/session
	// IDs, cookies, client metadata or authorization from the incoming request.
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Chatgpt-Account-Id", accountID)
	req.Header.Set("X-Openai-Account-Id", accountID)
	req.Header.Set("X-Basispoints-Auth-Mode", "chatgpt")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Origin", "https://bps.openai.com")
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("X-Openai-Internal-Basispoints-Client-Product", "basispoints-excel-plugin")
	req.Header.Set("X-Openai-Internal-Basispoints-Client-Agent-Profile", "excel")
	return req, nil
}

// prepareExcelBPSRequest is shared by real requests, connection tests and
// evaluations so tests exercise the same protocol as production inference.
func prepareExcelBPSRequest(ctx context.Context, raw []byte, token string, account *Account, scope string, cache bool) (*http.Request, *basispoints.Bridge, error) {
	if err := validateExcelOAuthAccount(account, token); err != nil {
		return nil, nil, err
	}
	var prepared []byte
	var bridge *basispoints.Bridge
	var err error
	if cache {
		prepared, bridge, err = basispoints.PrepareWithCatalog(raw, scope, &excelBPSReplay, &excelBPSCatalog)
	} else {
		prepared, bridge, err = basispoints.Prepare(raw, scope, nil)
	}
	if err != nil {
		return nil, nil, err
	}
	req, err := newExcelBPSRequest(ctx, prepared, token, excelOAuthAccountID(token))
	return req, bridge, err
}

// Sibling agents share a session header but have distinct threads. Reuse the
// existing execution-scope resolver so their tool catalogs and replay histories
// never overwrite each other, while each thread remains stable across turns.
func excelBPSConversationScope(c *gin.Context, body []byte, accountID int64) (string, bool) {
	identity, _ := resolveOpenAIWSExecutionScope(c, body, getAPIKeyIDFromContext(c))
	if identity == "" {
		return "", false
	}
	digest := sha256.Sum256([]byte(fmt.Sprintf("%d:%s", accountID, identity)))
	return hex.EncodeToString(digest[:]), true
}

func (s *OpenAIGatewayService) forwardExcelBPS(ctx context.Context, c *gin.Context, account *Account, body []byte, start time.Time) (*OpenAIForwardResult, error) {
	if GetOpenAIClientTransport(c) == OpenAIClientTransportWS || isOpenAIResponsesCompactPath(c) {
		return nil, excelBPSUnsupported(c)
	}
	restriction := s.detectCodexClientRestriction(c, account, body)
	if restriction.Enabled && !restriction.Matched {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"type": "forbidden_error", "message": CodexClientRestrictionMessage(restriction)}})
		return nil, errors.New("codex_cli_only restriction: only codex official clients are allowed")
	}
	originalModel := gjson.GetBytes(body, "model").String()
	billingModel, model := resolveOpenAIForwardMappedModels(account, originalModel, false)
	mapped, err := sjson.SetBytes(body, "model", model)
	if err != nil {
		return nil, err
	}
	token, _, err := s.GetAccessToken(ctx, account)
	if err != nil {
		return nil, err
	}
	scope, cache := excelBPSConversationScope(c, body, account.ID)
	req, bridge, err := prepareExcelBPSRequest(ctx, mapped, token, account, scope, cache)
	if err != nil {
		status := http.StatusBadRequest
		diagnostic := gin.H{"type": "invalid_request_error", "code": "basispoints_request_invalid", "message": err.Error()}
		var contentErr *basispoints.ContentValidationError
		if errors.As(err, &contentErr) {
			diagnostic["param"] = contentErr.Path
		}
		var accountErr *infraerrors.ApplicationError
		if errors.As(err, &accountErr) {
			status = int(accountErr.Code)
			diagnostic["code"], diagnostic["message"] = accountErr.Reason, accountErr.Message
		}
		c.JSON(status, gin.H{"error": diagnostic})
		return nil, err
	}
	setOpenAIExecContract(c, mapped, true)
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	SetActualOpenAIUpstreamEndpoint(c, "/basispoints/api/responses")
	SetOpsUpstreamModel(c, model)
	resp, err := s.doAccountRPMUpstream(req, proxyURL, account)
	if err != nil {
		if IsAccountRPMError(err) || ctx.Err() != nil {
			return nil, err
		}
		return nil, s.handleOpenAIUpstreamTransportError(ctx, c, account, err, false)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		raw := redactExcelBPSCredentialBody(s.readUpstreamErrorBody(resp), account, token)
		message := sanitizeUpstreamErrorMessage(extractUpstreamErrorMessage(raw))
		resp.Body = io.NopCloser(bytes.NewReader(raw))
		if s.shouldFailoverOpenAIUpstreamResponse(account, resp.StatusCode, message, raw) {
			shouldDisable := s.handleFailoverSideEffects(ctx, resp, account, raw, model)
			return nil, s.newOpenAIAccountFailoverError(account, resp.StatusCode, resp.Header, raw, message, shouldDisable, !shouldDisable && account.IsPoolMode() && account.IsPoolModeRetryableStatus(resp.StatusCode))
		}
		return s.handleErrorResponse(ctx, resp, c, account, mapped, model)
	}
	// The bridge owns a successful upstream body, including cancellation and
	// cleanup. Do not also close the original body outside its once guard.
	converted := bridge.StreamWithToolRepair(ctx, resp.Body, nil)
	defer converted.Close()
	response := *resp
	response.Body = converted
	response.Header = resp.Header.Clone()
	response.Header.Set("Content-Type", "text/event-stream")
	result := &OpenAIForwardResult{Model: originalModel, BillingModel: billingModel, UpstreamModel: model,
		UpstreamEndpoint: "/basispoints/api/responses", RequestID: resp.Header.Get("x-request-id"),
		Stream: gjson.GetBytes(body, "stream").Bool(), ReasoningEffort: &bridge.Effort, RequestedReasoningEffort: &bridge.RequestedEffort,
		UpstreamHeaders: resp.Header}
	if result.Stream {
		outcome, streamErr := s.handleStreamingResponseWithReasoning(ctx, &response, c, account, start, originalModel, model, bridge.Effort)
		if outcome != nil {
			outcome.applyForwardOutcome(result)
			if outcome.usage != nil {
				result.Usage = *outcome.usage
			}
			result.FirstTokenMs, result.ResponseID = outcome.firstTokenMs, outcome.responseID
		}
		err = streamErr
	} else {
		outcome, readErr := s.handleNonStreamingResponse(ctx, &response, c, account, originalModel, model)
		if outcome != nil {
			outcome.outcome.applyForwardOutcome(result)
			if outcome.usage != nil {
				result.Usage = *outcome.usage
			}
			result.ResponseID = outcome.responseID
		}
		err = readErr
	}
	result.Duration = time.Since(start)
	result.UpstreamResponseModel = observedUpstreamResponseModel(c)
	result.UpstreamResponseModelConflict = observedUpstreamResponseModelConflict(c)
	s.bindHTTPResponseAccount(ctx, c, account, result.ResponseID)
	return result, err
}

// convertExcelBPSTestRequest never adds Codex metadata or invokes a Codex plugin.
func convertExcelBPSTestRequest(req *http.Request, account *Account) (*http.Request, *basispoints.Bridge, error) {
	if req.Body == nil || strings.Contains(req.URL.Path, "compact") || strings.Contains(req.URL.Path, "images") {
		return nil, nil, excelBPSUnsupported(nil)
	}
	raw, err := io.ReadAll(io.LimitReader(req.Body, (16<<20)+1))
	_ = req.Body.Close()
	if err != nil || len(raw) > 16<<20 {
		return nil, nil, errors.New("BPS test request could not be read or exceeds the size limit")
	}
	token := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
	return prepareExcelBPSRequest(req.Context(), raw, token, account, "", false)
}

func bridgeExcelBPSTestResponse(ctx context.Context, resp *http.Response, bridge *basispoints.Bridge) *http.Response {
	if resp != nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		resp.Body = bridge.StreamWithToolRepair(ctx, resp.Body, nil)
		resp.Header = resp.Header.Clone()
		resp.Header.Set("Content-Type", "text/event-stream")
	}
	return resp
}
