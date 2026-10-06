package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/prismbridge/bridge"
	"github.com/Wei-Shaw/sub2api/internal/pkg/prismbridge/prism"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// PrismResponseStateStore is implemented by the existing Redis gateway cache.
// References are scoped to API key, selected account and verified owner.
// Nothing is persisted in a second account database or a process-local map.
type PrismResponseStateStore interface {
	GetPrismResponse(context.Context, string) ([]byte, error)
	PutPrismResponse(context.Context, string, []byte, time.Duration) error
}

type prismResponseState struct {
	Items []json.RawMessage `json:"items"`
	Tools json.RawMessage   `json:"tools,omitempty"`
}

// Private dependency seam keeps full forwarding tests on a local HTTP server
// without permitting account configuration to redirect authenticated traffic.
type prismGatewayRuntime struct {
	resolveModel func(context.Context, *Account, string, string) (string, string, error)
	principal    func(context.Context, *Account) (*prism.Client, prism.Principal, func(), error)
}

func prismResponseScope(keyID int64, account *Account) string {
	// Rotating a token for the same verified principal must not discard its
	// full tool history. Generic edits cannot change the verified identity.
	if identity := prismString(account.Credentials, "prism_verified_identity"); identity != "" {
		digest := sha256.Sum256([]byte(identity))
		return fmt.Sprintf("%d:%d:%s:", keyID, account.ID, hex.EncodeToString(digest[:]))
	}
	credentials, _ := json.Marshal(account.Credentials)
	digest := sha256.Sum256(credentials)
	return fmt.Sprintf("%d:%d:%s:", keyID, account.ID, hex.EncodeToString(digest[:]))
}

// RunPrismText is shared with supported quality evaluations. Their existing
// caller owns admission; this helper never acquires another slot or retries.
func RunPrismText(ctx context.Context, account *Account, model, effort, prompt string) (*prism.StatusResponse, error) {
	if len(prompt) > bridge.PromptLimit {
		return nil, errors.New("context_length_exceeded")
	}
	model, effort, err := ResolvePrismAccountModel(ctx, account, model, effort)
	if err != nil {
		return nil, err
	}
	client, p, closeClient, err := PrismAccountPrincipal(ctx, account)
	if err != nil {
		return nil, err
	}
	defer closeClient()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	return (bridge.Runner{Client: client}).Run(ctx, p, []prism.InputItem{prism.NewUserItem(prompt)}, model, effort)
}

func prismGatewayError(c *gin.Context, status int, code, message string) error {
	if !c.Writer.Written() {
		c.JSON(status, gin.H{"error": gin.H{"type": "invalid_request_error", "code": code, "message": message}})
	}
	return fmt.Errorf("prism: %s", code)
}

func (s *OpenAIGatewayService) forwardPrismResponses(ctx context.Context, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error) {
	return s.forwardPrism(ctx, c, account, body, false)
}

func (s *OpenAIGatewayService) forwardPrismChat(ctx context.Context, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error) {
	converted, err := prismChatToResponses(body)
	if err != nil {
		return nil, prismGatewayError(c, 400, "unsupported_parameter", err.Error())
	}
	return s.forwardPrism(ctx, c, account, converted, true)
}

func (s *OpenAIGatewayService) forwardPrism(ctx context.Context, c *gin.Context, account *Account, body []byte, chat bool) (*OpenAIForwardResult, error) {
	started := time.Now()
	if strings.Contains(strings.ToLower(c.Request.URL.Path), "compact") || strings.Contains(strings.ToLower(c.GetHeader("X-Codex-Request-Kind")), "compact") || strings.Contains(strings.ToLower(c.GetHeader("X-OpenAI-Request-Kind")), "compact") || bridge.IsCompactMetadata(c.GetHeader("X-Codex-Turn-Metadata")) {
		return nil, prismGatewayError(c, 400, "unsupported_operation", "Prism does not support compact")
	}
	req, err := bridge.Parse(body)
	if err != nil {
		return nil, prismGatewayError(c, 400, "unsupported_parameter", err.Error())
	}
	items, err := bridge.InputItems(req.Input)
	if err != nil {
		return nil, prismGatewayError(c, 400, "unsupported_parameter", err.Error())
	}
	if s.prismAccountService != nil {
		account, err = s.prismAccountService.EnsureFresh(ctx, account)
		if err != nil {
			return nil, prismGatewayError(c, 502, "upstream_authentication_error", "Prism credentials could not be refreshed")
		}
	}
	keyID := getAPIKeyIDFromContext(c)
	if keyID <= 0 {
		return nil, prismGatewayError(c, 401, "invalid_api_key", "Prism requests require an authenticated API key")
	}
	store, _ := s.cache.(PrismResponseStateStore)
	shouldStore := !chat && string(req.Raw["store"]) != "false"
	if (shouldStore || req.PreviousResponseID != "") && store == nil {
		return nil, prismGatewayError(c, 503, "state_unavailable", "Prism response state storage is unavailable")
	}
	scope := prismResponseScope(keyID, account)
	if req.PreviousResponseID != "" {
		if !strings.HasPrefix(req.PreviousResponseID, "resp_prism_") || len(req.PreviousResponseID) > 100 {
			return nil, prismGatewayError(c, 400, "invalid_previous_response_id", "Unknown Prism response reference")
		}
		raw, getErr := store.GetPrismResponse(ctx, scope+req.PreviousResponseID)
		if getErr != nil {
			return nil, prismGatewayError(c, 503, "state_unavailable", "Prism response state could not be read")
		}
		var prior prismResponseState
		if len(raw) == 0 || json.Unmarshal(raw, &prior) != nil {
			return nil, prismGatewayError(c, 400, "invalid_previous_response_id", "Prism response reference expired or belongs to another key, account or verified owner")
		}
		items = append(prior.Items, items...)
		if len(req.Tools) == 0 || string(req.Tools) == "null" {
			req.Tools = prior.Tools
			req.Raw["tools"] = prior.Tools
		}
	}
	input, toolBridge, err := bridge.Prepare(req, items)
	if err != nil {
		code := "unsupported_parameter"
		if strings.Contains(err.Error(), "context_length_exceeded") {
			code = "context_length_exceeded"
		}
		return nil, prismGatewayError(c, 400, code, err.Error())
	}
	resolveModel, principal := ResolvePrismAccountModel, PrismAccountPrincipal
	if s.prismRuntime != nil {
		if s.prismRuntime.resolveModel != nil {
			resolveModel = s.prismRuntime.resolveModel
		}
		if s.prismRuntime.principal != nil {
			principal = s.prismRuntime.principal
		}
	}
	model, effort, err := resolveModel(ctx, account, req.Model, req.Reasoning.Effort)
	if err != nil {
		return nil, prismGatewayError(c, 400, "model_not_supported", "Requested Prism model or reasoning effort is unavailable for this account")
	}
	// Native transport does not use repository.HTTPUpstream's admission wrapper.
	// Reserve the same account RPM once before any inference work; the handler
	// owns the concurrency slot and handles typed RPM failover/wait decisions.
	if err := s.admitAccountRPM(ctx, account); err != nil {
		return nil, err
	}
	client, p, closeClient, err := principal(ctx, account)
	if err != nil {
		return nil, prismGatewayError(c, 502, "upstream_authentication_error", "Prism account credentials are unavailable")
	}
	defer closeClient()
	runCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	responseID := "resp_prism_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	result := &OpenAIForwardResult{ResponseID: responseID, Model: req.Model, UpstreamModel: model, BillingModel: model, ReasoningEffort: &effort, Stream: req.Stream, UpstreamEndpoint: prism.PathResponseStart, ResponsesOutcomeObserved: true}
	type completion struct {
		status *prism.StatusResponse
		err    error
	}
	done := make(chan completion, 1)
	type observed struct {
		status *prism.StatusResponse
		reply  chan error
	}
	observations := make(chan observed)
	var observer func(*prism.StatusResponse) error
	if req.Stream && !toolBridge {
		observer = func(st *prism.StatusResponse) error {
			reply := make(chan error, 1)
			select {
			case observations <- observed{st, reply}:
			case <-runCtx.Done():
				return runCtx.Err()
			}
			select {
			case e := <-reply:
				return e
			case <-runCtx.Done():
				return runCtx.Err()
			}
		}
	}
	go func() {
		st, e := (bridge.Runner{Client: client, Observe: observer}).Run(runCtx, p, input, model, effort)
		done <- completion{st, e}
	}()
	var st *prism.StatusResponse
	var stream *prismEventWriter
	if req.Stream {
		stream = &prismEventWriter{c: c, chat: chat}
		stream.begin()
	}
	progress := &prismProgress{w: stream, id: responseID, model: req.Model}
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	waiting := true
	for waiting {
		select {
		case observation := <-observations:
			e := progress.observe(observation.status)
			if len(progress.lines) > 0 && result.FirstTokenMs == nil {
				first := int(time.Since(started).Milliseconds())
				result.FirstTokenMs = &first
				result.ResponsesMeaningfulOutput = true
			}
			observation.reply <- e
		case got := <-done:
			st, err = got.status, got.err
			waiting = false
		case <-ticker.C:
			if stream != nil {
				if e := stream.heartbeat(); e != nil {
					cancel()
					err = e
				}
			}
		}
	}
	result.Duration = time.Since(started)
	if st != nil {
		result.RequestID = st.RequestID
		if st.Usage != nil {
			result.Usage.InputTokens = st.Usage.InputTokens
			result.Usage.OutputTokens = st.Usage.OutputTokens
		}
	}
	if err != nil {
		result.ResponsesProtocolStatus = "failed"
		result.ResponsesStatus = "failed"
		if ctx.Err() != nil {
			result.ClientDisconnect = true
			result.ResponsesProtocolStatus = "cancelled"
		}
		if stream != nil {
			_ = stream.failure(responseID, req.Model)
		} else {
			_ = prismGatewayError(c, 502, "upstream_error", "Prism generation failed; no completed response was produced")
		}
		return result, fmt.Errorf("prism execution failed: %w", err)
	}
	text := st.Text
	if toolBridge && !strings.Contains(text, "```codex-exec") {
		if js := bridge.SynthesizeDeltaFilesExecJS(st.DeltaFiles, strings.Contains(strings.ToLower(c.GetHeader("User-Agent")), "windows")); js != "" {
			text = "```codex-exec\n" + js + "\n```"
		} else if bridge.IsFauxSandboxCompletion(text) && !prismHasPriorToolOutput(items) {
			result.ResponsesProtocolStatus = "failed"
			result.ResponsesStatus = "failed"
			if stream != nil {
				_ = stream.failure(responseID, req.Model)
			} else {
				_ = prismGatewayError(c, 502, "tool_bridge_error", "Prism claimed a remote sandbox action without a client tool call")
			}
			return result, errors.New("Prism tool bridge returned an unverified sandbox completion")
		}
	}
	output, err := bridge.Output(req, text, toolBridge, strings.TrimPrefix(responseID, "resp_prism_"))
	if err != nil {
		result.ResponsesProtocolStatus = "failed"
		result.ResponsesStatus = "failed"
		if stream != nil {
			_ = stream.failure(responseID, req.Model)
		} else {
			_ = prismGatewayError(c, 502, "tool_bridge_error", "Prism returned an invalid tool call")
		}
		return result, err
	}
	finalOutput := output
	if len(progress.lines) > 0 {
		if err := progress.finish(); err != nil {
			result.ClientDisconnect = true
			return result, err
		}
		if text == progress.texts[progress.lines[len(progress.lines)-1]] {
			finalOutput = nil
		}
		output = append(append([]json.RawMessage(nil), progress.items...), finalOutput...)
		stream.progressStarted = true
		stream.outputOffset = len(progress.items)
	}
	if shouldStore {
		state, _ := json.Marshal(prismResponseState{Items: append(items, output...), Tools: req.Tools})
		if err = store.PutPrismResponse(ctx, scope+responseID, state, 24*time.Hour); err != nil {
			result.ResponsesProtocolStatus = "failed"
			result.ResponsesStatus = "failed"
			if stream != nil {
				_ = stream.failure(responseID, req.Model)
			} else {
				_ = prismGatewayError(c, 503, "state_unavailable", "Prism response could not be stored")
			}
			return result, errors.New("Prism state persistence failed")
		}
	}
	response := prismResponseObject(responseID, req.Model, output, st.Usage)
	if shouldStore {
		s.bindHTTPResponseAccount(ctx, c, account, responseID)
	}
	if chat {
		chatText := st.Text
		if len(progress.lines) > 0 {
			if chatText == progress.texts[progress.lines[len(progress.lines)-1]] {
				chatText = ""
			} else {
				chatText = "\n" + chatText
			}
		}
		err = prismWriteChat(c, stream, responseID, req.Model, chatText, st.Usage)
	} else if stream != nil {
		err = stream.completed(response, finalOutput)
	} else {
		c.JSON(http.StatusOK, response)
	}
	result.ResponsesProtocolStatus = "completed"
	result.ResponsesStatus = "completed"
	result.UpstreamTerminalEvent = "response.completed"
	result.ResponsesMeaningfulOutput = len(output) > 0
	for _, item := range output {
		var v struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(item, &v)
		if v.Type == "function_call" || v.Type == "custom_tool_call" {
			result.ResponsesToolCallForwarded = true
			result.ExecCallObserved = true
		}
	}
	if err != nil {
		result.ClientDisconnect = true
		return result, err
	}
	if result.FirstTokenMs == nil {
		first := int(time.Since(started).Milliseconds())
		result.FirstTokenMs = &first
	}
	return result, nil
}

func prismHasPriorToolOutput(items []json.RawMessage) bool {
	for _, raw := range items {
		var item struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(raw, &item)
		if item.Type == "custom_tool_call_output" || item.Type == "function_call_output" {
			return true
		}
	}
	return false
}
