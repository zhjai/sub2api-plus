package service

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

const openAIStateProbeVersion = "codex-turn-state-v1"

// OpenAIStateProbeResult deliberately excludes bearer, ticket, cookies and output.
// A changed ticket is routing evidence, not proof of a model capability change.
type OpenAIStateProbeResult struct {
	Version              string                   `json:"version"`
	Verdict              string                   `json:"verdict"`
	Failure              string                   `json:"failure,omitempty"`
	RequestCount         int                      `json:"request_count"`
	MintStatus           int                      `json:"mint_status,omitempty"`
	ContinueStatus       int                      `json:"continue_status,omitempty"`
	TicketLength         int                      `json:"ticket_length,omitempty"`
	ContinueTicketLength int                      `json:"continue_ticket_length,omitempty"`
	NewTicket            bool                     `json:"new_ticket"`
	ReportedModel        string                   `json:"reported_model,omitempty"`
	LatencyMS            int64                    `json:"latency_ms"`
	RetryPolicy          string                   `json:"retry_policy"`
	Samples              []OpenAIEvalSampleRecord `json:"samples,omitempty"`
}

type openAIStateProbeShot struct {
	status   int
	ticket   string
	cookies  string
	model    string
	terminal bool
	failure  string
	record   OpenAIEvalSampleRecord
}

func isOpenAIStateProbeTarget(target *OpenAIEvalTarget) bool {
	if target == nil || target.Account == nil || target.Credential == nil {
		return false
	}
	account, credential := target.Account, target.Credential
	return account.IsOpenAIOAuth() && !account.IsShadow() && !account.IsSyntheticUITest() && credential.Type == AccountTypeOAuth && !credential.IsOpenAIAgentIdentity()
}

func (s *AccountTestService) RunOpenAIStateProbe(ctx context.Context, target *OpenAIEvalTarget) *OpenAIStateProbeResult {
	start := time.Now()
	result := &OpenAIStateProbeResult{Version: openAIStateProbeVersion, Verdict: "inconclusive", RetryPolicy: "unsupported_linked_ticket_chain"}
	defer func() { result.LatencyMS = time.Since(start).Milliseconds() }()
	if s == nil || target == nil || target.Account == nil || target.Credential == nil || s.openaiGatewayService == nil || s.httpUpstream == nil {
		result.Failure = "unavailable"
		return result
	}
	account, credential := target.Account, target.Credential
	if !isOpenAIStateProbeTarget(target) {
		result.Failure = "unsupported_account"
		return result
	}
	token, _, err := s.openaiGatewayService.GetAccessToken(ctx, credential)
	if err != nil || strings.TrimSpace(token) == "" {
		result.Failure = "credential_unavailable"
		message := "OAuth access token is unavailable"
		if err != nil {
			message = err.Error()
		}
		result.Samples = []OpenAIEvalSampleRecord{{ProbeID: "state-probe-mint", ErrorCode: result.Failure, ErrorMessage: sanitizeOpenAIEvalText(message, openAIEvalErrorLimit, openAIEvalCredentialSecrets(credential)...)}}
		return result
	}
	mint := s.openAIStateProbeShot(ctx, account, credential, token, target.UpstreamModel, "", "")
	mint.record.ProbeID = "state-probe-mint"
	result.Samples = append(result.Samples, mint.record)
	result.RequestCount += mint.record.Attempts
	result.MintStatus = mint.status
	result.TicketLength = len(mint.ticket)
	if mint.failure != "" {
		result.Failure = mint.failure
		return result
	}
	if mint.ticket == "" {
		result.Failure = "missing_ticket"
		return result
	}
	continued := s.openAIStateProbeShot(ctx, account, credential, token, target.UpstreamModel, mint.ticket, mint.cookies)
	continued.record.ProbeID = "state-probe-continue"
	result.Samples = append(result.Samples, continued.record)
	result.RequestCount += continued.record.Attempts
	result.ContinueStatus = continued.status
	result.ContinueTicketLength = len(continued.ticket)
	result.ReportedModel = continued.model
	if result.ReportedModel == "" {
		result.ReportedModel = mint.model
	}
	if continued.failure != "" {
		result.Failure = continued.failure
		return result
	}
	if continued.ticket == "" {
		result.Failure = "missing_ticket"
		return result
	}
	result.NewTicket = continued.ticket != "" && continued.ticket != mint.ticket
	if result.NewTicket {
		result.Verdict = "degraded"
	} else {
		result.Verdict = "healthy"
	}
	return result
}

func (s *AccountTestService) openAIStateProbeShot(ctx context.Context, account, credential *Account, token, model, ticket, cookies string) (out openAIStateProbeShot) {
	secrets := append(openAIEvalCredentialSecrets(credential), token, ticket, cookies)
	defer func() {
		out.model = sanitizeOpenAIEvalText(out.model, 160, secrets...)
		if out.failure != "" {
			if out.record.ErrorCode == "" {
				out.record.ErrorCode = out.failure
			}
			if out.record.ErrorMessage == "" {
				out.record.ErrorMessage = strings.ReplaceAll(out.failure, "_", " ")
			}
			out.record.ErrorMessage = sanitizeOpenAIEvalText(out.record.ErrorMessage, openAIEvalErrorLimit, secrets...)
			out.record.ErrorCode = sanitizeOpenAIEvalText(out.record.ErrorCode, 80, secrets...)
			out.record.AttemptErrors = []OpenAIEvalAttemptError{{Attempt: out.record.Attempts, Code: out.record.ErrorCode, Message: out.record.ErrorMessage, HTTPStatus: out.status}}
		}
		out.record.Valid = out.failure == ""
		out.record.HTTPStatus = out.status
	}()
	shotCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	payload := map[string]any{
		"model": model, "instructions": "Reply with OK.", "input": "Reply with OK.",
		"stream": true, "store": false, "include": []string{"reasoning.encrypted_content"},
	}
	applyCodexOAuthTransform(payload, true, false)
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(shotCtx, http.MethodPost, chatgptCodexAPIURL, bytes.NewReader(body))
	if err != nil {
		out.failure = "request_invalid"
		return out
	}
	req = req.WithContext(WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileOpenAI))
	req.Host = "chatgpt.com"
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("OpenAI-Beta", "responses=experimental")
	enforceCodexAcceptLanguage(req.Header)
	req.Header.Set("session_id", uuid.NewString())
	setOpenAIChatGPTAccountHeaders(req.Header, credential)
	enforceCodexIdentityHeadersWithUA(req.Header, credential.GetOpenAIUserAgent())
	credential.ApplyHeaderOverrides(req.Header)
	enforceCodexAcceptLanguage(req.Header)
	if ticket != "" {
		req.Header.Set(openAICodexTurnStateHeader, ticket)
	}
	if cookies != "" {
		req.Header.Set("Cookie", cookies)
	}
	for key, values := range req.Header {
		key = strings.ToLower(key)
		if strings.Contains(key, "auth") || strings.Contains(key, "cookie") || strings.Contains(key, "key") || strings.Contains(key, "token") || strings.Contains(key, "state") {
			secrets = append(secrets, values...)
		}
	}
	proxy := ""
	if account.Proxy != nil {
		proxy = account.Proxy.URL()
	}
	if ctx.Err() != nil {
		out.failure = "cancelled"
		return out
	}
	out.record.Attempts = 1
	resp, err := s.doOpenAIEvalUpstream(req, proxy, account, credential)
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		out.failure = "network_error"
		failure := openAIEvalIOError(shotCtx, err, 0)
		if !failure.Attempted {
			out.record.Attempts = 0
			out.failure = failure.Code
		}
		out.record.ErrorCode, out.record.ErrorMessage = failure.Code, failure.Message
		return out
	}
	if resp == nil || resp.Body == nil {
		out.failure = "stream_error"
		return out
	}
	defer func() { _ = resp.Body.Close() }()
	stopClose := context.AfterFunc(shotCtx, func() { _ = resp.Body.Close() })
	defer stopClose()
	out.status = resp.StatusCode
	if resp.StatusCode != http.StatusOK {
		switch resp.StatusCode {
		case http.StatusTooManyRequests:
			out.failure = "rate_limited"
		case http.StatusUnauthorized, http.StatusForbidden:
			out.failure = "account_error"
		default:
			out.failure = "upstream_error"
		}
		_, responseErr := readOpenAIEvalResponse(shotCtx, resp, true, false)
		if responseErr != nil {
			out.record.ErrorCode = safeOpenAIEvalErrorCode(responseErr)
			out.record.ErrorMessage = responseErr.Error()
		}
		return out
	}
	out.ticket = extractOpenAICodexTurnState(resp.Header)
	secrets = append(secrets, out.ticket)
	var routeCookies []string
	for _, cookie := range resp.Cookies() {
		if (cookie.Name == "__cflb" || cookie.Name == "__oailb") && cookie.Value != "" && cookie.MaxAge >= 0 {
			routeCookies = append(routeCookies, cookie.Name+"="+cookie.Value)
		}
	}
	out.cookies = strings.Join(routeCookies, "; ")
	for _, cookie := range resp.Cookies() {
		secrets = append(secrets, cookie.Value)
	}
	response, responseErr := readOpenAIEvalResponse(shotCtx, resp, true, false)
	if response != nil {
		out.model = response.Model
	}
	if responseErr != nil {
		out.failure = "stream_error"
		out.record.ErrorCode = safeOpenAIEvalErrorCode(responseErr)
		out.record.ErrorMessage = responseErr.Error()
		if out.record.ErrorCode == "missing_terminal" {
			out.failure = "missing_terminal"
		}
	} else {
		out.terminal = true
	}
	if out.failure == "" && out.terminal && out.ticket == "" {
		out.failure = "missing_ticket"
	}
	if out.failure == "" && !out.terminal {
		out.failure = "missing_terminal"
	}
	return out
}
