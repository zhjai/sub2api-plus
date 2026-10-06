package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	Attempts             int                      `json:"attempts"`
	MaxAttempts          int                      `json:"max_attempts"`
	LastFailureStep      string                   `json:"last_failure_step,omitempty"`
	Samples              []OpenAIEvalSampleRecord `json:"samples,omitempty"`
}

type openAIStateProbeShot struct {
	admissionErr error
	status       int
	ticket       string
	cookies      string
	model        string
	terminal     bool
	failure      string
	record       OpenAIEvalSampleRecord
	retryable    bool
	retryAfter   time.Duration
}

func isOpenAIStateProbeTarget(target *OpenAIEvalTarget) bool {
	if target == nil || target.Account == nil || target.Credential == nil {
		return false
	}
	account, credential := target.Account, target.Credential
	return account.IsOpenAIOAuth() && !account.IsShadow() && !account.IsSyntheticUITest() && credential.Type == AccountTypeOAuth && !credential.IsOpenAIAgentIdentity()
}

func (s *AccountTestService) RunOpenAIStateProbe(ctx context.Context, target *OpenAIEvalTarget) *OpenAIStateProbeResult {
	return s.RunOpenAIStateProbeAttempts(ctx, target, OpenAIEvalDefaultMaxRequestAttempts)
}

func (s *AccountTestService) RunOpenAIStateProbeAttempts(ctx context.Context, target *OpenAIEvalTarget, maximum int) *OpenAIStateProbeResult {
	start := time.Now()
	// A probe attempt is a fresh mint/continue chain. Keep the total request
	// budget bounded at three chains (six sends), even when an older caller
	// supplies a larger value.
	maximum = max(1, min(maximum, 3))
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	result := &OpenAIStateProbeResult{Version: openAIStateProbeVersion, Verdict: "inconclusive", RetryPolicy: "fresh_linked_ticket_chain", MaxAttempts: maximum}
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
	for attempt := 1; attempt <= maximum; attempt++ {
		if ctx.Err() != nil {
			result.Failure = "cancelled"
			break
		}
		result.Attempts = attempt
		result.ContinueStatus, result.ContinueTicketLength = 0, 0
		result.NewTicket = false
		session := uuid.NewString()
		mint := s.openAIStateProbeShot(ctx, account, credential, token, target.UpstreamModel, "", "", session)
		mint.record.ProbeID = fmt.Sprintf("state-probe-%d-mint", attempt)
		result.Samples = append(result.Samples, mint.record)
		result.RequestCount += mint.record.Attempts
		result.MintStatus, result.TicketLength = mint.status, len(mint.ticket)
		result.ReportedModel = mint.model
		failed := mint
		result.LastFailureStep = "mint"
		if mint.failure == "" {
			continued := s.openAIStateProbeShot(ctx, account, credential, token, target.UpstreamModel, mint.ticket, mint.cookies, session)
			continued.record.ProbeID = fmt.Sprintf("state-probe-%d-continue", attempt)
			result.Samples = append(result.Samples, continued.record)
			result.RequestCount += continued.record.Attempts
			result.ContinueStatus, result.ContinueTicketLength = continued.status, len(continued.ticket)
			if continued.model != "" {
				result.ReportedModel = continued.model
			}
			failed = continued
			result.LastFailureStep = "continue"
			if continued.failure == "" {
				result.Failure, result.LastFailureStep = "", ""
				result.NewTicket = continued.ticket != mint.ticket
				result.Verdict = "healthy"
				if result.NewTicket {
					result.Verdict = "degraded"
				}
				return result
			}
		}
		result.Failure = failed.failure
		if !failed.retryable || attempt == maximum {
			break
		}
		delay := time.Duration(attempt) * 100 * time.Millisecond
		if failed.retryAfter > delay {
			delay = failed.retryAfter
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			result.Failure = "cancelled"
			return result
		case <-timer.C:
		}
	}
	return result
}

func (s *AccountTestService) openAIStateProbeShot(ctx context.Context, account, credential *Account, token, model, ticket, cookies string, sessions ...string) (out openAIStateProbeShot) {
	deadline := time.Now().Add(2 * time.Minute)
	for {
		out = s.openAIStateProbeSingleSend(ctx, account, credential, token, model, ticket, cookies, sessions...)
		if retry, err := waitOpenAIEvalAdmission(ctx, deadline, out.admissionErr); retry {
			continue
		} else if err != nil {
			out.failure, out.record.ErrorCode, out.record.ErrorMessage = safeOpenAIEvalErrorCode(err), safeOpenAIEvalErrorCode(err), err.Error()
		}
		return out
	}
}

func (s *AccountTestService) openAIStateProbeSingleSend(ctx context.Context, account, credential *Account, token, model, ticket, cookies string, sessions ...string) (out openAIStateProbeShot) {
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
	release, err := s.acquireOpenAIEvalAccountSlot(ctx, account)
	if err != nil {
		out.failure = safeOpenAIEvalErrorCode(err)
		out.record.ErrorMessage = err.Error()
		return out
	}
	defer release()
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
	session := uuid.NewString()
	if len(sessions) > 0 {
		session = sessions[0]
	}
	req.Header.Set("session_id", session)
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
		var admission *AccountRPMError
		if errors.As(err, &admission) {
			out.admissionErr = err
			out.record.Attempts = 0
			out.failure = "account_rpm_admission"
			return out
		}
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		out.failure = "network_error"
		failure := openAIEvalIOError(shotCtx, err, 0)
		out.retryable = failure.Retryable
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
	if reset := parseRetryAfterResetTime(resp.Header, time.Now()); reset != nil {
		out.retryAfter = time.Until(*reset)
	}
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
			out.retryable = openAIStateProbeRetryable(responseErr)
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
		out.retryable = openAIStateProbeRetryable(responseErr)
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
		out.retryable = true
	}
	if out.failure == "" && !out.terminal {
		out.failure = "missing_terminal"
	}
	return out
}

func openAIStateProbeRetryable(err error) bool {
	var failure *OpenAIEvalRequestError
	return errors.As(err, &failure) && failure.Retryable
}
