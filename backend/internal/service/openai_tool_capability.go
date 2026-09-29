package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

const openAIToolCapabilityContextKey = "openai_tool_capability_state"

const openAIToolCapabilityLoggedKey = "openai_tool_capability_failure_logged"

const openAIExecProtocolPrefixMaxBytes = 8 * 1024

const OpenAIExecProtocolLeakReason GatewayFailureReason = "openai_exec_protocol_leak"

type openAIExecProtocolVerdict uint8

const (
	openAIExecProtocolPending openAIExecProtocolVerdict = iota
	openAIExecProtocolOrdinary
	openAIExecProtocolLeak
)

type openAIExecProtocolGuard struct {
	enabled  bool
	verdict  openAIExecProtocolVerdict
	text     string
	seenText bool
}

type openAIToolCapabilityState struct {
	ClientExecDeclared   bool
	OutboundExecDeclared bool
	ExecCallObserved     bool
	ExplicitDenial       bool
	ProtocolLeakObserved bool
	TextWindow           string
	OutputTextWindow     string
}

// setOpenAIExecContract records the client and current outbound tool contract.
// It is intentionally request-scoped; capability degradation is never inferred
// from an account's static metadata.
func setOpenAIExecContract(c *gin.Context, body []byte, outbound bool) {
	if c == nil {
		return
	}
	state := openAIToolCapabilityState{}
	if raw, ok := c.Get(openAIToolCapabilityContextKey); ok {
		state, _ = raw.(openAIToolCapabilityState)
	}
	declared := responsesBodyDeclaresExec(body)
	if c.Request != nil && !outbound {
		c.Request = c.Request.WithContext(withOpenAIExecCapability(c.Request.Context(), declared))
	}
	if outbound {
		state.OutboundExecDeclared = declared
		state.ExecCallObserved = false
		state.ExplicitDenial = false
		state.ProtocolLeakObserved = false
		state.TextWindow = ""
		state.OutputTextWindow = ""
		c.Set(openAIToolCapabilityLoggedKey, false)
	} else {
		state = openAIToolCapabilityState{ClientExecDeclared: declared}
	}
	c.Set(openAIToolCapabilityContextKey, state)
}

func openAIToolCapabilityStateFromContext(c *gin.Context) openAIToolCapabilityState {
	if c == nil {
		return openAIToolCapabilityState{}
	}
	value, _ := c.Get(openAIToolCapabilityContextKey)
	state, _ := value.(openAIToolCapabilityState)
	return state
}

func newOpenAIExecProtocolGuard(c *gin.Context) *openAIExecProtocolGuard {
	state := openAIToolCapabilityStateFromContext(c)
	return &openAIExecProtocolGuard{enabled: state.ClientExecDeclared && state.OutboundExecDeclared && isNativeOpenAIResponsesRequest(c)}
}

func isNativeOpenAIResponsesRequest(c *gin.Context) bool {
	if c == nil || c.Request == nil || c.Request.URL == nil {
		return false
	}
	switch strings.TrimRight(strings.TrimSpace(c.Request.URL.Path), "/") {
	case "/v1/responses", "/openai/v1/responses", "/responses", "/backend-api/codex/responses":
		return true
	default:
		return false
	}
}

func (g *openAIExecProtocolGuard) pending() bool {
	return g != nil && g.enabled && g.verdict == openAIExecProtocolPending
}

func (g *openAIExecProtocolGuard) observe(eventType string, data []byte, startsClientOutput, execCallObserved bool) openAIExecProtocolVerdict {
	if g == nil || !g.enabled || g.verdict == openAIExecProtocolLeak {
		return openAIExecProtocolOrdinary
	}
	if execCallObserved {
		g.verdict = openAIExecProtocolOrdinary
		g.text = ""
		return g.verdict
	}
	fragment := openAIAssistantOutputText(eventType, data, g.seenText)
	if g.seenText && (eventType == "response.output_text.done" || eventType == "response.content_part.done" || eventType == "response.output_item.done") {
		fullText := openAIAssistantOutputText(eventType, data, false)
		if fullText != "" {
			g.text = ""
			g.verdict = openAIExecProtocolOrdinary
			fragment = fullText
		}
	}
	if fragment != "" {
		g.seenText = true
		g.observeText(fragment)
	}
	if g.pending() && (eventType == "response.output_text.done" || eventType == "response.content_part.done" ||
		eventType == "response.output_item.done" || openAIStreamEventTypeIsTerminal(eventType) ||
		(startsClientOutput && fragment == "" && !g.seenText)) {
		g.verdict = openAIExecProtocolOrdinary
		g.text = ""
	}
	return g.verdict
}

func (g *openAIExecProtocolGuard) observeText(fragment string) {
	for len(fragment) > 0 {
		lineEnd := strings.IndexByte(fragment, '\n')
		lineBytes := len(fragment)
		if lineEnd >= 0 {
			lineBytes = lineEnd + 1
		}
		if lineBytes > openAIExecProtocolPrefixMaxBytes-len(g.text) {
			g.text = ""
			g.verdict = openAIExecProtocolOrdinary
			fragment = fragment[lineBytes:]
			continue
		}
		g.text += fragment[:lineBytes]
		g.verdict = classifyLeakedExecProtocol(g.text)
		fragment = fragment[lineBytes:]
		if g.verdict == openAIExecProtocolLeak {
			return
		}
		if g.verdict == openAIExecProtocolOrdinary && lineEnd >= 0 {
			g.text = ""
		}
	}
}

func openAIAssistantOutputText(eventType string, data []byte, alreadySeen bool) string {
	switch strings.TrimSpace(eventType) {
	case "response.output_text.delta":
		return gjson.GetBytes(data, "delta").String()
	case "response.output_text.done":
		if !alreadySeen {
			return gjson.GetBytes(data, "text").String()
		}
	case "response.content_part.added", "response.content_part.done":
		if !alreadySeen && gjson.GetBytes(data, "part.type").String() == "output_text" {
			return gjson.GetBytes(data, "part.text").String()
		}
	case "response.output_item.added", "response.output_item.done":
		if !alreadySeen && gjson.GetBytes(data, "item.type").String() == "message" {
			var text strings.Builder
			for _, part := range gjson.GetBytes(data, "item.content").Array() {
				if part.Get("type").String() == "output_text" {
					text.WriteString(part.Get("text").String())
				}
			}
			return text.String()
		}
	}
	return ""
}

func observeOpenAIToolCapabilitySSE(c *gin.Context, eventType string, data []byte) {
	if c == nil {
		return
	}
	capability := openAIToolCapabilityStateFromContext(c)
	typ := strings.TrimSpace(eventType)
	itemType := strings.TrimSpace(gjson.GetBytes(data, "item.type").String())
	name := strings.TrimSpace(gjson.GetBytes(data, "name").String())
	if name == "" {
		name = strings.TrimSpace(gjson.GetBytes(data, "item.name").String())
	}
	if (itemType == "custom_tool_call" || itemType == "function_call" || strings.Contains(typ, "tool_call") || strings.Contains(typ, "function_call")) && strings.EqualFold(name, "exec") {
		capability.ExecCallObserved = true
	}
	// Only assistant-generated output text can prove a tool-call protocol leak.
	// A request echo, error payload, or quoted input must not poison the route.
	generatedText := openAIAssistantOutputText(typ, data, capability.OutputTextWindow != "")
	if generatedText != "" {
		capability.OutputTextWindow += generatedText
		if len(capability.OutputTextWindow) > 4096 {
			capability.OutputTextWindow = capability.OutputTextWindow[len(capability.OutputTextWindow)-4096:]
		}
		if leakedExecProtocol(capability.OutputTextWindow) {
			capability.ProtocolLeakObserved = true
		}
		capability.TextWindow += generatedText
		if len(capability.TextWindow) > 1024 {
			capability.TextWindow = capability.TextWindow[len(capability.TextWindow)-1024:]
		}
		if explicitExecDenial(capability.TextWindow) {
			capability.ExplicitDenial = true
		}
	}
	c.Set(openAIToolCapabilityContextKey, capability)
}

func openAIToolCapabilityFailure(c *gin.Context, completedTerminal bool) bool {
	if !completedTerminal {
		return false
	}
	if c == nil {
		return false
	}
	capability := openAIToolCapabilityStateFromContext(c)
	return capability.ClientExecDeclared && capability.OutboundExecDeclared &&
		(capability.ExplicitDenial || capability.ProtocolLeakObserved) && !capability.ExecCallObserved
}

func openAIToolCapabilityStrongLeak(c *gin.Context) bool {
	capability := openAIToolCapabilityStateFromContext(c)
	return capability.ClientExecDeclared && capability.OutboundExecDeclared &&
		capability.ProtocolLeakObserved && !capability.ExecCallObserved
}

func logOpenAIToolCapabilityFailure(c *gin.Context, account *Account, model string) {
	if c == nil {
		return
	}
	if logged, _ := c.Get(openAIToolCapabilityLoggedKey); logged == true {
		return
	}
	state, _ := c.Get(openAIToolCapabilityContextKey)
	capability, _ := state.(openAIToolCapabilityState)
	accountID := int64(0)
	accountType := ""
	if account != nil {
		accountID = account.ID
		accountType = account.Type
	}
	logCtx := context.Background()
	if c.Request != nil {
		logCtx = c.Request.Context()
	}
	logger.FromContext(logCtx).Warn("openai_tool_capability_failure",
		zap.Int64("account_id", accountID),
		zap.String("account_type", accountType),
		zap.String("model", strings.TrimSpace(model)),
		zap.Bool("client_exec_declared", capability.ClientExecDeclared),
		zap.Bool("outbound_exec_declared", capability.OutboundExecDeclared),
		zap.Bool("exec_call_observed", capability.ExecCallObserved),
		zap.Bool("explicit_denial", capability.ExplicitDenial),
		zap.Bool("protocol_leak_observed", capability.ProtocolLeakObserved),
	)
	c.Set(openAIToolCapabilityLoggedKey, true)
}

func leakedExecProtocol(text string) bool {
	return classifyLeakedExecProtocol(text) == openAIExecProtocolLeak
}

func classifyLeakedExecProtocol(text string) openAIExecProtocolVerdict {
	if len(text) > openAIExecProtocolPrefixMaxBytes {
		return openAIExecProtocolOrdinary
	}
	trimmed := strings.TrimLeft(text, " \t\r\n")
	if trimmed == "" {
		return openAIExecProtocolPending
	}
	leading := text[:len(text)-len(trimmed)]
	if strings.Contains(leading, "\t") || len(leading)-strings.LastIndex(leading, "\n")-1 >= 4 {
		return openAIExecProtocolOrdinary
	}
	for _, header := range []string{
		"to=functions.exec code:\n", "to=functions.exec code:\r\n",
		"to=functions.exec:\n", "to=functions.exec:\r\n",
		"to=container.exec code:\n", "to=container.exec code:\r\n",
		"to=container.exec:\n", "to=container.exec:\r\n",
	} {
		if strings.HasPrefix(header, trimmed) {
			return openAIExecProtocolPending
		}
		if !strings.HasPrefix(trimmed, header) {
			continue
		}
		body := strings.TrimLeft(trimmed[len(header):], " \t\r\n")
		if body == "" || body == "{" {
			return openAIExecProtocolPending
		}
		if !strings.HasPrefix(body, "{") {
			return openAIExecProtocolOrdinary
		}
		var fields map[string]json.RawMessage
		err := json.NewDecoder(strings.NewReader(body)).Decode(&fields)
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return openAIExecProtocolPending
		}
		if err != nil {
			return openAIExecProtocolOrdinary
		}
		for _, key := range []string{"cmd", "command", "commands", "input"} {
			if raw := fields[key]; len(raw) > 0 && openAIExecProtocolCommand(raw) {
				return openAIExecProtocolLeak
			}
		}
		return openAIExecProtocolOrdinary
	}
	return openAIExecProtocolOrdinary
}

func openAIExecProtocolCommand(raw json.RawMessage) bool {
	var command string
	if json.Unmarshal(raw, &command) == nil {
		return strings.TrimSpace(command) != ""
	}
	var commands []string
	if json.Unmarshal(raw, &commands) != nil || len(commands) == 0 {
		return false
	}
	for _, item := range commands {
		if strings.TrimSpace(item) != "" {
			return true
		}
	}
	return false
}

func responsesBodyDeclaresExec(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	var visit func(gjson.Result) bool
	visit = func(value gjson.Result) bool {
		if value.IsObject() {
			typ := strings.TrimSpace(value.Get("type").String())
			name := strings.TrimSpace(value.Get("name").String())
			if (typ == "custom" || typ == "function") && strings.EqualFold(name, "exec") {
				return true
			}
		}
		if value.IsObject() || value.IsArray() {
			found := false
			value.ForEach(func(_, child gjson.Result) bool {
				if visit(child) {
					found = true
					return false
				}
				return true
			})
			return found
		}
		return false
	}
	return visit(gjson.ParseBytes(body))
}

func explicitExecDenial(text string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return false
	}
	for _, nonDenial := range []string{
		"没有必要", "不需要", "无需", "不必", "no need", "not necessary", "don't need", "do not need",
	} {
		if strings.Contains(text, nonDenial) {
			return false
		}
	}
	// Do not combine a generic negation with the generic word "tool". Text such
	// as "没有必要调用工具" describes the model's choice, not a missing
	// capability. Require an explicit workspace/terminal capability object.
	denial := strings.Contains(text, "没有") ||
		strings.Contains(text, "无可用") ||
		strings.Contains(text, "未提供") ||
		strings.Contains(text, "不可用") ||
		strings.Contains(text, "无法访问") ||
		strings.Contains(text, "not available") ||
		strings.Contains(text, "unavailable") ||
		strings.Contains(text, "not provided") ||
		strings.Contains(text, "not exposed") ||
		strings.Contains(text, "don't have") ||
		strings.Contains(text, "do not have") ||
		strings.Contains(text, "cannot access") ||
		strings.Contains(text, "can't access") ||
		strings.Contains(text, "no terminal") ||
		strings.Contains(text, "no exec") ||
		strings.Contains(text, "no shell") ||
		strings.Contains(text, "no filesystem") ||
		strings.Contains(text, "no file system") ||
		strings.Contains(text, "no workspace tool")
	capability := strings.Contains(text, "终端") ||
		strings.Contains(text, "文件系统") ||
		strings.Contains(text, "工作区工具") ||
		strings.Contains(text, "工具入口") ||
		strings.Contains(text, "执行工具") ||
		strings.Contains(text, "terminal") ||
		strings.Contains(text, "exec") ||
		strings.Contains(text, "shell") ||
		strings.Contains(text, "filesystem") ||
		strings.Contains(text, "file system") ||
		strings.Contains(text, "workspace tool") ||
		strings.Contains(text, "tool entry")
	return denial && capability
}
