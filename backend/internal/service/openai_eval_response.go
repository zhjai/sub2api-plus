package service

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/util/logredact"
	"golang.org/x/net/http2"
)

const openAIEvalResponseLimit = 2 << 20
const openAIEvalAnswerLimit = 16000
const openAIEvalErrorLimit = 1000

type openAIEvalFullCandyAnswerKey struct{}

func withOpenAIEvalFullCandyAnswer(ctx context.Context) context.Context {
	return context.WithValue(ctx, openAIEvalFullCandyAnswerKey{}, true)
}

func openAIEvalAnswerLimitFor(ctx context.Context) int {
	if ctx != nil && ctx.Value(openAIEvalFullCandyAnswerKey{}) == true {
		// Keep the whole accepted Candy reply for the collapsed annotation.
		// The wire parser still rejects responses exceeding its existing bound.
		return openAIEvalResponseLimit
	}
	return openAIEvalAnswerLimit
}

// Only classified, recoverable request failures may be retried. Attempted is
// false for local validation/token failures that made no inference request.
type OpenAIEvalRequestError struct {
	Code       string
	Message    string
	HTTPStatus int
	Retryable  bool
	Attempted  bool
}

func (e *OpenAIEvalRequestError) Error() string {
	if e.HTTPStatus != 0 {
		return fmt.Sprintf("%s (HTTP %d): %s", e.Code, e.HTTPStatus, e.Message)
	}
	return e.Code + ": " + e.Message
}

var openAIEvalURLPattern = regexp.MustCompile(`(?i)\b(?:https?|socks5h?)://[^\s<>"']+`)
var openAIEvalBearerPattern = regexp.MustCompile(`(?i)\b(?:bearer|basic)\s+[^\s,;"']+`)
var openAIEvalTokenPattern = regexp.MustCompile(`\b(?:sk-[A-Za-z0-9_-]+|eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+)`)
var openAIEvalHeaderPattern = regexp.MustCompile(`(?im)\b(?:authorization|proxy-authorization|cookie|set-cookie|x-api-key|x-codex-turn-state)\s*[:=][^\r\n]+`)
var openAIEvalCodePattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_.-]{0,79}$`)

func sanitizeOpenAIEvalText(value string, limit int, secrets ...string) string {
	for _, secret := range secrets {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "[redacted]")
			encoded, _ := json.Marshal(secret)
			if len(encoded) > 2 {
				value = strings.ReplaceAll(value, string(encoded[1:len(encoded)-1]), "[redacted]")
			}
		}
	}
	value = openAIEvalURLPattern.ReplaceAllString(value, "[url redacted]")
	value = openAIEvalHeaderPattern.ReplaceAllString(value, "[header redacted]")
	value = openAIEvalBearerPattern.ReplaceAllString(value, "[authorization redacted]")
	value = openAIEvalTokenPattern.ReplaceAllString(value, "[token redacted]")
	value = logredact.RedactText(value, "api_key", "api-key", "authorization", "cookie", "set-cookie", "token", "ticket", "encrypted_content", "client_assertion")
	value = strings.Map(func(r rune) rune {
		if r < 32 && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, value)
	if len(value) > limit {
		value = value[:limit-3]
		for !utf8.ValidString(value) {
			value = value[:len(value)-1]
		}
		value += "..."
	}
	return value
}

func openAIEvalCredentialSecrets(account *Account) []string {
	if account == nil {
		return nil
	}
	var secrets []string
	var collect func(any)
	collect = func(v any) {
		switch v := v.(type) {
		case string:
			if v != "" {
				secrets = append(secrets, v)
			}
		case map[string]any:
			for _, item := range v {
				collect(item)
			}
		case map[string]string:
			for _, item := range v {
				collect(item)
			}
		case []any:
			for _, item := range v {
				collect(item)
			}
		}
	}
	for key, value := range account.Credentials {
		key = strings.ToLower(key)
		if strings.Contains(key, "token") || strings.Contains(key, "secret") || strings.Contains(key, "key") || strings.Contains(key, "cookie") || strings.Contains(key, "password") || strings.Contains(key, "header") {
			collect(value)
		}
	}
	return secrets
}

func newOpenAIEvalRequestError(code, message string, status int) *OpenAIEvalRequestError {
	if !openAIEvalCodePattern.MatchString(code) {
		code = "upstream_error"
	}
	e := &OpenAIEvalRequestError{Code: code, Message: message, HTTPStatus: status, Attempted: true}
	// Authentication, quota, malformed requests and output truncation are
	// deterministic even if an upstream incorrectly attaches a 5xx status.
	lower := strings.ToLower(code)
	for _, denied := range []string{"auth", "api_key", "permission", "forbidden", "invalid", "unsupported", "not_found", "quota", "billing", "balance", "context_length", "max_output", "content_filter", "refusal"} {
		if strings.Contains(lower, denied) {
			return e
		}
	}
	if status == 401 || status == 403 || (status >= 400 && status < 500 && status != 408 && status != 429) {
		return e
	}
	switch lower {
	case "rate_limit", "rate_limit_exceeded", "rate_limited", "server_error", "server_overloaded", "server_is_overloaded", "overloaded_error", "temporarily_unavailable", "timeout", "network_error", "stream_read_error", "missing_terminal":
		e.Retryable = true
	}
	if status == 408 || status == 429 || status == 500 || status == 502 || status == 503 || status == 504 {
		e.Retryable = true
	}
	return e
}

func openAIEvalIOError(ctx context.Context, err error, status int) *OpenAIEvalRequestError {
	var unsupported *HTTPUpstreamSingleSendUnsupportedError
	if errors.As(err, &unsupported) {
		return &OpenAIEvalRequestError{Code: "single_send_unsupported", Message: unsupported.Error()}
	}
	code := "network_error"
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	if errors.Is(err, context.Canceled) {
		code = "cancelled"
	} else if errors.Is(err, context.DeadlineExceeded) {
		code = "timeout"
	} else {
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			code = "timeout"
		}
	}
	e := newOpenAIEvalRequestError(code, err.Error(), status)
	if code == "cancelled" {
		e.Retryable = false
		return e
	}
	if code == "network_error" {
		var netErr net.Error
		var streamErr http2.StreamError
		var goAwayErr http2.GoAwayError
		e.Retryable = errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
			errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED) ||
			errors.Is(err, syscall.EPIPE) || (errors.As(err, &netErr) && (netErr.Timeout() || netErr.Temporary())) ||
			(errors.As(err, &streamErr) && streamErr.Code == http2.ErrCodeRefusedStream) || errors.As(err, &goAwayErr) ||
			strings.Contains(err.Error(), "http2: Transport received GOAWAY from server") ||
			strings.Contains(err.Error(), "http2: Transport received Server's graceful shutdown GOAWAY")
		if !e.Retryable {
			e.Code = "request_failed"
		}
	}
	// Certificate/configuration errors are not recoverable transport failures.
	if strings.Contains(strings.ToLower(err.Error()), "certificate") || strings.Contains(strings.ToLower(err.Error()), "unsupported protocol") {
		e.Retryable = false
	}
	return e
}

type openAIEvalWireError struct {
	Code    string `json:"code"`
	Type    string `json:"type"`
	Message string `json:"message"`
}
type openAIEvalWireResponse struct {
	Status string `json:"status"`
	Model  string `json:"model"`
	Output []struct {
		Type    string `json:"type"`
		Content []struct {
			Type    string `json:"type"`
			Text    string `json:"text"`
			Refusal string `json:"refusal"`
		} `json:"content"`
	} `json:"output"`
	Usage struct {
		InputTokens  int64 `json:"input_tokens"`
		OutputTokens int64 `json:"output_tokens"`
	} `json:"usage"`
	Error             *openAIEvalWireError `json:"error"`
	IncompleteDetails struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
}

func openAIEvalWireFailure(wire *openAIEvalWireError, fallback, message string, status int) *OpenAIEvalRequestError {
	code := fallback
	if wire != nil {
		if wire.Code != "" {
			code = wire.Code
		} else if wire.Type != "" {
			code = wire.Type
		}
		if wire.Message != "" {
			message = wire.Message
		}
	}
	return newOpenAIEvalRequestError(code, message, status)
}

func openAIEvalResponseText(wire *openAIEvalWireResponse) (string, string) {
	var text, refusal strings.Builder
	for _, item := range wire.Output {
		if item.Type != "message" {
			continue
		}
		for _, content := range item.Content {
			if content.Type == "output_text" {
				text.WriteString(content.Text)
			}
			if content.Type == "refusal" {
				refusal.WriteString(content.Refusal)
				if content.Refusal == "" {
					refusal.WriteString("model refused the request")
				}
			}
		}
	}
	return text.String(), refusal.String()
}

// Reads Responses JSON or real SSE frames. Only response.completed commits a
// stream; output_item.done and [DONE] cannot turn partial output into success.
func readOpenAIEvalResponse(ctx context.Context, resp *http.Response, requireSSE, requireText bool) (*OpenAIEvalSampleResponse, error) {
	result := &OpenAIEvalSampleResponse{HTTPStatus: resp.StatusCode}
	limited := &io.LimitedReader{R: resp.Body, N: openAIEvalResponseLimit + 1}
	reader := bufio.NewReader(limited)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, err := io.ReadAll(reader)
		if ctx.Err() != nil {
			return result, openAIEvalIOError(ctx, ctx.Err(), resp.StatusCode)
		}
		if err != nil {
			return result, openAIEvalIOError(ctx, err, resp.StatusCode)
		}
		var wire struct {
			Error   *openAIEvalWireError `json:"error"`
			Message string               `json:"message"`
		}
		_ = json.Unmarshal(body, &wire)
		message := wire.Message
		if message == "" {
			message = string(body)
		}
		if strings.TrimSpace(message) == "" {
			message = http.StatusText(resp.StatusCode)
		}
		return result, openAIEvalWireFailure(wire.Error, fmt.Sprintf("http_%d", resp.StatusCode), message, resp.StatusCode)
	}
	// Some compatible providers omit Content-Type; sniff only the first bytes.
	prefix, _ := reader.Peek(5)
	isSSE := requireSSE || strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") || strings.HasPrefix(string(prefix), "data:") || strings.HasPrefix(string(prefix), "event") || strings.HasPrefix(string(prefix), ":")
	complete := func(wire *openAIEvalWireResponse, terminal string, delta, refusal string) (*OpenAIEvalSampleResponse, error) {
		result.Model = wire.Model
		text, wireRefusal := openAIEvalResponseText(wire)
		if text != "" {
			result.Text = text
		} else {
			result.Text = delta
		}
		result.InputTokens, result.OutputTokens = wire.Usage.InputTokens, wire.Usage.OutputTokens
		if wire.Error != nil || terminal != "completed" || (wire.Status != "" && wire.Status != "completed") {
			if terminal == "completed" && wire.Status != "" {
				terminal = wire.Status
			}
			code := "response_" + terminal
			message := "OpenAI evaluation response did not complete: " + terminal
			if wire.IncompleteDetails.Reason != "" {
				code = wire.IncompleteDetails.Reason
				message += ": " + code
			}
			return result, openAIEvalWireFailure(wire.Error, code, message, resp.StatusCode)
		}
		if wireRefusal != "" {
			refusal = wireRefusal
		}
		if refusal != "" {
			return result, newOpenAIEvalRequestError("refusal", refusal, resp.StatusCode)
		}
		if requireText && strings.TrimSpace(result.Text) == "" {
			return result, newOpenAIEvalRequestError("empty_output", "completed response contained no output text", resp.StatusCode)
		}
		result.CompletedAt = time.Now().UTC()
		return result, nil
	}
	if !isSSE {
		body, err := io.ReadAll(reader)
		if ctx.Err() != nil {
			return result, openAIEvalIOError(ctx, ctx.Err(), resp.StatusCode)
		}
		if err != nil {
			return result, openAIEvalIOError(ctx, err, resp.StatusCode)
		}
		if limited.N == 0 {
			return result, newOpenAIEvalRequestError("response_too_large", "response exceeded 2 MiB", resp.StatusCode)
		}
		var wire openAIEvalWireResponse
		if json.Unmarshal(body, &wire) != nil {
			return result, newOpenAIEvalRequestError("invalid_response", "upstream returned invalid Responses JSON", resp.StatusCode)
		}
		return complete(&wire, wire.Status, "", "")
	}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), openAIEvalResponseLimit+1)
	var data, delta, refusal strings.Builder
	eventName := ""
	dispatch := func() (bool, error) {
		if data.Len() == 0 {
			eventName = ""
			return false, nil
		}
		raw := strings.TrimSpace(data.String())
		data.Reset()
		if raw == "[DONE]" {
			return false, newOpenAIEvalRequestError("missing_terminal", "stream ended before response.completed", resp.StatusCode)
		}
		var event struct {
			Type     string                  `json:"type"`
			Delta    string                  `json:"delta"`
			Response *openAIEvalWireResponse `json:"response"`
			Error    *openAIEvalWireError    `json:"error"`
			Code     string                  `json:"code"`
			Message  string                  `json:"message"`
		}
		if json.Unmarshal([]byte(raw), &event) != nil {
			return false, newOpenAIEvalRequestError("invalid_response", "invalid SSE event JSON", resp.StatusCode)
		}
		if event.Type == "" {
			event.Type = eventName
		}
		eventName = ""
		switch event.Type {
		case "response.output_text.delta":
			delta.WriteString(event.Delta)
			result.Text = delta.String()
		case "response.refusal.delta":
			refusal.WriteString(event.Delta)
		case "response.completed", "response.failed", "response.incomplete", "response.cancelled":
			if event.Response == nil {
				return true, openAIEvalWireFailure(event.Error, "invalid_response", "terminal SSE event omitted response", resp.StatusCode)
			}
			if event.Response.Error == nil {
				event.Response.Error = event.Error
			}
			_, err := complete(event.Response, strings.TrimPrefix(event.Type, "response."), delta.String(), refusal.String())
			return true, err
		case "error":
			if event.Error == nil {
				event.Error = &openAIEvalWireError{Code: event.Code, Message: event.Message}
			}
			return true, openAIEvalWireFailure(event.Error, "response_failed", "upstream sent an error event", resp.StatusCode)
		}
		return false, nil
	}
	for scanner.Scan() {
		if ctx.Err() != nil {
			return result, openAIEvalIOError(ctx, ctx.Err(), resp.StatusCode)
		}
		if limited.N == 0 {
			return result, newOpenAIEvalRequestError("response_too_large", "stream exceeded 2 MiB", resp.StatusCode)
		}
		line := scanner.Text()
		if line == "" {
			terminal, err := dispatch()
			if terminal || err != nil {
				return result, err
			}
			continue
		}
		if strings.HasPrefix(line, "event:") {
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		}
		if strings.HasPrefix(line, "data:") {
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
			data.WriteByte('\n')
		}
	}
	if ctx.Err() != nil {
		return result, openAIEvalIOError(ctx, ctx.Err(), resp.StatusCode)
	}
	if limited.N == 0 {
		return result, newOpenAIEvalRequestError("response_too_large", "stream exceeded 2 MiB", resp.StatusCode)
	}
	if err := scanner.Err(); err != nil {
		return result, openAIEvalIOError(ctx, err, resp.StatusCode)
	}
	if terminal, err := dispatch(); terminal || err != nil {
		return result, err
	}
	return result, newOpenAIEvalRequestError("missing_terminal", "premature EOF before response.completed", resp.StatusCode)
}

// RunOpenAIEvalSampleAttempts owns the retry budget; each sample dispatch can
// transmit at most once, independently of whether the account has an RPM limit.
func (s *AccountTestService) RunOpenAIEvalSampleAttempts(ctx context.Context, target *OpenAIEvalTarget, prompt, effort string, maximum int) (result *OpenAIEvalSampleResponse, record OpenAIEvalSampleRecord, err error) {
	var inputTokens, outputTokens int64
	defer func() {
		if result == nil && (inputTokens != 0 || outputTokens != 0) {
			result = &OpenAIEvalSampleResponse{}
		}
		if result != nil {
			result.InputTokens, result.OutputTokens = inputTokens, outputTokens
		}
	}()
	for attempt := 0; attempt < maximum; attempt++ {
		if ctx.Err() != nil {
			err = openAIEvalIOError(ctx, ctx.Err(), 0)
			break
		}
		result, err = s.RunOpenAIEvalSample(ctx, target, prompt, effort)
		var failure *OpenAIEvalRequestError
		if err == nil || (errors.As(err, &failure) && failure.Attempted) {
			record.Attempts++
		}
		if result != nil {
			inputTokens += result.InputTokens
			outputTokens += result.OutputTokens
			record.Answer = sanitizeOpenAIEvalText(result.Text, openAIEvalAnswerLimitFor(ctx))
			record.HTTPStatus = result.HTTPStatus
		}
		if err == nil {
			record.Valid = true
			record.ErrorCode = ""
			record.ErrorMessage = ""
			return result, record, nil
		}
		record.ErrorCode = safeOpenAIEvalErrorCode(err)
		record.ErrorMessage = sanitizeOpenAIEvalText(err.Error(), openAIEvalErrorLimit)
		if failure != nil {
			record.HTTPStatus = failure.HTTPStatus
		}
		record.AttemptErrors = append(record.AttemptErrors, OpenAIEvalAttemptError{Attempt: record.Attempts, Code: record.ErrorCode, Message: record.ErrorMessage, HTTPStatus: record.HTTPStatus})
		if failure == nil || !failure.Retryable || ctx.Err() != nil || attempt+1 == maximum {
			break
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			err = openAIEvalIOError(ctx, ctx.Err(), 0)
		case <-timer.C:
			continue
		}
		break
	}
	if err != nil && record.ErrorMessage == "" {
		record.ErrorCode = safeOpenAIEvalErrorCode(err)
		record.ErrorMessage = sanitizeOpenAIEvalText(err.Error(), openAIEvalErrorLimit)
	}
	return result, record, err
}

func (s *AccountTestService) runOpenAIEvalSampleAttempts(ctx context.Context, target *OpenAIEvalTarget, prompt, effort string, maximum int) (*OpenAIEvalSampleResponse, OpenAIEvalSampleRecord, error) {
	return s.RunOpenAIEvalSampleAttempts(ctx, target, prompt, effort, maximum)
}
