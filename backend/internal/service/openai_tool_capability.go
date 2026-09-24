package service

import (
	"context"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

const openAIToolCapabilityContextKey = "openai_tool_capability_state"

const openAIToolCapabilityLoggedKey = "openai_tool_capability_failure_logged"

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
	} else {
		state = openAIToolCapabilityState{ClientExecDeclared: declared}
	}
	c.Set(openAIToolCapabilityContextKey, state)
}

func observeOpenAIToolCapabilitySSE(c *gin.Context, eventType string, data []byte) {
	if c == nil {
		return
	}
	state, _ := c.Get(openAIToolCapabilityContextKey)
	capability, _ := state.(openAIToolCapabilityState)
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
	generatedText := ""
	switch typ {
	case "response.output_text.delta":
		generatedText = gjson.GetBytes(data, "delta").String()
	case "response.output_text.done":
		if capability.OutputTextWindow == "" {
			generatedText = gjson.GetBytes(data, "text").String()
		}
	case "response.output_item.done":
		if itemType == "message" && capability.OutputTextWindow == "" {
			generatedText = gjson.GetBytes(data, "item.content.0.text").String()
		}
	}
	if generatedText != "" {
		capability.OutputTextWindow += generatedText
		if len(capability.OutputTextWindow) > 4096 {
			capability.OutputTextWindow = capability.OutputTextWindow[len(capability.OutputTextWindow)-4096:]
		}
		if leakedExecProtocol(capability.OutputTextWindow) {
			capability.ProtocolLeakObserved = true
		}
	}
	for _, text := range []string{
		gjson.GetBytes(data, "delta").String(),
		gjson.GetBytes(data, "text").String(),
		gjson.GetBytes(data, "item.content.0.text").String(),
		gjson.GetBytes(data, "response.output_text").String(),
	} {
		if text != "" {
			capability.TextWindow += text
			if len(capability.TextWindow) > 1024 {
				capability.TextWindow = capability.TextWindow[len(capability.TextWindow)-1024:]
			}
		}
		if explicitExecDenial(text) {
			capability.ExplicitDenial = true
		}
	}
	if explicitExecDenial(capability.TextWindow) {
		capability.ExplicitDenial = true
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
	state, _ := c.Get(openAIToolCapabilityContextKey)
	capability, ok := state.(openAIToolCapabilityState)
	return ok && capability.ClientExecDeclared && capability.OutboundExecDeclared &&
		(capability.ExplicitDenial || capability.ProtocolLeakObserved) && !capability.ExecCallObserved
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

// leakedExecProtocol deliberately recognizes only a complete, leading raw
// tool-call envelope with a JSON cmd. Explanations, Markdown code fences, and
// ordinary mentions of exec are not evidence of a broken tool channel.
func leakedExecProtocol(text string) bool {
	text = strings.TrimLeft(text, " \t\r\n")
	const prefix = "to=functions.exec code:"
	if !strings.HasPrefix(text, prefix) {
		return false
	}
	text = strings.TrimLeft(strings.TrimPrefix(text, prefix), " \t")
	if !strings.HasPrefix(text, "\n") && !strings.HasPrefix(text, "\r\n") {
		return false
	}
	text = strings.TrimSpace(text)
	if !gjson.Valid(text) {
		return false
	}
	cmd := gjson.Get(text, "cmd")
	return cmd.Type == gjson.String && strings.TrimSpace(cmd.String()) != ""
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
