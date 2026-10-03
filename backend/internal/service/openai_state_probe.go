package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

const openAIStateProbeVersion = "codex-turn-state-v1"

// OpenAIStateProbeResult deliberately excludes bearer, ticket, cookies and output.
// A changed ticket is routing evidence, not proof of a model capability change.
type OpenAIStateProbeResult struct {
	Version              string `json:"version"`
	Verdict              string `json:"verdict"`
	Failure              string `json:"failure,omitempty"`
	RequestCount         int    `json:"request_count"`
	MintStatus           int    `json:"mint_status,omitempty"`
	ContinueStatus       int    `json:"continue_status,omitempty"`
	TicketLength         int    `json:"ticket_length,omitempty"`
	ContinueTicketLength int    `json:"continue_ticket_length,omitempty"`
	NewTicket            bool   `json:"new_ticket"`
	ReportedModel        string `json:"reported_model,omitempty"`
	LatencyMS            int64  `json:"latency_ms"`
}

type openAIStateProbeShot struct {
	status   int
	ticket   string
	cookies  string
	model    string
	terminal bool
	failure  string
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
	result := &OpenAIStateProbeResult{Version: openAIStateProbeVersion, Verdict: "inconclusive"}
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
		return result
	}
	mint := s.openAIStateProbeShot(ctx, account, credential, token, target.UpstreamModel, "", "")
	result.RequestCount++
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
	result.RequestCount++
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

func (s *AccountTestService) openAIStateProbeShot(ctx context.Context, account, credential *Account, token, model, ticket, cookies string) openAIStateProbeShot {
	out := openAIStateProbeShot{}
	shotCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	body, _ := json.Marshal(map[string]any{
		"model": model, "instructions": "Reply with OK.", "input": "Reply with OK.",
		"stream": true, "store": false, "include": []string{"reasoning.encrypted_content"},
	})
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
	if ticket != "" {
		req.Header.Set(openAICodexTurnStateHeader, ticket)
	}
	if cookies != "" {
		req.Header.Set("Cookie", cookies)
	}
	proxy := ""
	if account.Proxy != nil {
		proxy = account.Proxy.URL()
	}
	resp, err := s.httpUpstream.DoWithTLS(req, proxy, account.ID, account.Concurrency, s.tlsFPProfileService.ResolveTLSProfile(account))
	if err != nil {
		out.failure = "network_error"
		return out
	}
	if resp == nil || resp.Body == nil {
		out.failure = "stream_error"
		return out
	}
	defer func() { _ = resp.Body.Close() }()
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
		return out
	}
	out.ticket = extractOpenAICodexTurnState(resp.Header)
	var routeCookies []string
	for _, cookie := range resp.Cookies() {
		if (cookie.Name == "__cflb" || cookie.Name == "__oailb") && cookie.Value != "" && cookie.MaxAge >= 0 {
			routeCookies = append(routeCookies, cookie.Name+"="+cookie.Value)
		}
	}
	out.cookies = strings.Join(routeCookies, "; ")
	reader := bufio.NewScanner(io.LimitReader(resp.Body, 1<<20))
	reader.Buffer(make([]byte, 4096), 1<<20)
	for reader.Scan() {
		line := strings.TrimSpace(reader.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var event struct {
			Type     string `json:"type"`
			Response struct {
				Model string `json:"model"`
			} `json:"response"`
		}
		if json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &event) != nil {
			continue
		}
		if event.Response.Model != "" {
			out.model = event.Response.Model
		}
		switch event.Type {
		case "response.completed":
			out.terminal = true
		case "response.failed", "response.incomplete", "response.cancelled", "error":
			out.failure = "stream_error"
		}
	}
	if err := reader.Err(); err != nil && !errors.Is(err, context.Canceled) {
		out.failure = "stream_error"
	}
	if out.failure == "" && out.terminal && out.ticket == "" {
		out.failure = "missing_ticket"
	}
	if out.failure == "" && !out.terminal {
		out.failure = "missing_terminal"
	}
	return out
}
