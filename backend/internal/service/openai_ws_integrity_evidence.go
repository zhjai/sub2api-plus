package service

import (
	"net/http"
	"strings"
	"sync"

	"github.com/tidwall/gjson"
)

// Evidence is scoped to a single turn, including passthrough's concurrent relays.
type openAIWSIntegrityEvidence struct {
	mu                  sync.Mutex
	capability          openAIToolCapabilityState
	protocolGuard       openAIExecProtocolGuard
	status              string
	reason              string
	deliveredResponseID string
	deliveredTerminal   string
	meaningful          bool
	tool                bool
	committed           bool
	pendingFrames       [][]byte
	pendingBytes        int
}

func newOpenAIWSIntegrityEvidence(clientBody, outboundBody []byte) *openAIWSIntegrityEvidence {
	clientExec, outboundExec := responsesBodyDeclaresExec(clientBody), responsesBodyDeclaresExec(outboundBody)
	return &openAIWSIntegrityEvidence{
		capability:    openAIToolCapabilityState{ClientExecDeclared: clientExec, OutboundExecDeclared: outboundExec},
		protocolGuard: openAIExecProtocolGuard{enabled: clientExec && outboundExec},
	}
}

func (e *openAIWSIntegrityEvidence) observe(eventType string, payload []byte) {
	e.mu.Lock()
	defer e.mu.Unlock()
	observeOpenAIToolCapabilityState(&e.capability, eventType, payload)
	if e.protocolGuard.observe(eventType, payload, openAIStreamDataStartsClientOutput(string(payload), eventType), e.capability.ExecCallObserved) == openAIExecProtocolLeak {
		e.capability.ProtocolLeakObserved = true
	}
	if openAIStreamEventTypeIsTerminal(eventType) {
		e.status, _, e.reason = classifyOpenAIResponsesOutcome(eventType, payload)
	}
}

func (e *openAIWSIntegrityEvidence) delivered(eventType string, payload []byte) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if eventType != "keepalive" {
		e.committed = true
	}
	e.meaningful = e.meaningful || openAIStreamDataStartsVisibleOutput(string(payload), eventType)
	if _, id, _ := parseOpenAIWSEventEnvelope(payload); id != "" {
		e.deliveredResponseID = id
	}
	itemType := gjson.GetBytes(payload, "item.type").String()
	e.tool = e.tool || strings.Contains(eventType, "tool_call") || strings.Contains(eventType, "function_call") ||
		itemType == "function_call" || itemType == "custom_tool_call" || itemType == "tool_search_call"
	if openAIStreamEventTypeIsTerminal(eventType) {
		e.deliveredTerminal = eventType
		for _, item := range gjson.GetBytes(payload, "response.output").Array() {
			switch item.Get("type").String() {
			case "function_call", "custom_tool_call", "tool_search_call":
				e.tool = true
			}
		}
	}
}

// Binary delivery commits the turn, so any held text must precede it on the wire.
func (e *openAIWSIntegrityEvidence) takePendingFrames() [][]byte {
	e.mu.Lock()
	defer e.mu.Unlock()
	frames := e.pendingFrames
	e.pendingFrames, e.pendingBytes = nil, 0
	return frames
}

func (e *openAIWSIntegrityEvidence) terminalWasDelivered(eventType string) bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.deliveredTerminal == eventType
}

// Only potentially structured initial output is held. Once delivered, later
// leakage remains evidence and cannot cause replay of the current turn.
func (e *openAIWSIntegrityEvidence) prepareDelivery(eventType string, payload []byte) ([][]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.committed || !e.protocolGuard.enabled || eventType == "keepalive" {
		return [][]byte{payload}, nil
	}
	if e.protocolGuard.verdict == openAIExecProtocolLeak && !e.capability.ExecCallObserved {
		e.pendingFrames, e.pendingBytes = nil, 0
		return nil, newOpenAIExecProtocolLeakFailoverError()
	}
	if e.protocolGuard.pending() {
		if int64(e.pendingBytes)+int64(len(payload)) > openAIFirstOutputStageMaxBytes {
			e.pendingFrames, e.pendingBytes = nil, 0
			return nil, &UpstreamFailoverError{
				StatusCode: http.StatusBadGateway, SafeToFailoverAfterWrite: true,
				ResponseBody: []byte(`{"error":{"type":"upstream_error","message":"WebSocket first-output staging limit exceeded"}}`),
			}
		}
		e.pendingFrames = append(e.pendingFrames, append([]byte(nil), payload...))
		e.pendingBytes += len(payload)
		return nil, nil
	}
	frames := append(e.pendingFrames, payload)
	e.pendingFrames, e.pendingBytes = nil, 0
	return frames, nil
}

func (e *openAIWSIntegrityEvidence) apply(result *OpenAIForwardResult, terminated, cancelled bool) {
	if e == nil || result == nil {
		return
	}
	result.ClientDisconnect = cancelled
	if cancelled {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if result.RequestID == "" {
		result.RequestID = e.deliveredResponseID
	}
	status, reason := e.status, e.reason
	if terminated && status == "" && (e.meaningful || e.tool) {
		status, reason = "premature_eof", "stream_terminated"
	}
	result.ResponsesOutcomeObserved = status != ""
	result.ResponsesProtocolStatus, result.ResponsesIncompleteReason = status, reason
	result.ResponsesMeaningfulOutput, result.ResponsesToolCallForwarded = e.meaningful, e.tool
	result.ExecCallObserved = e.capability.ExecCallObserved
	result.PrecommitExecProtocolLeak = !e.committed && e.capability.ClientExecDeclared && e.capability.OutboundExecDeclared &&
		e.protocolGuard.verdict == openAIExecProtocolLeak && !e.capability.ExecCallObserved
	result.ToolCapabilityFailure = status == "completed" && e.capability.ClientExecDeclared && e.capability.OutboundExecDeclared &&
		(e.capability.ExplicitDenial || e.capability.ProtocolLeakObserved) && !e.capability.ExecCallObserved
}

func (e *openAIWSIntegrityEvidence) deliveredID() string {
	if e == nil {
		return ""
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.deliveredResponseID
}
