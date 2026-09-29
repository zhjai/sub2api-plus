package service

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestResponsesBodyDeclaresExecFindsNamespacedCustomTool(t *testing.T) {
	body := []byte(`{"tools":[{"type":"namespace","name":"functions","tools":[{"type":"custom","name":"exec"}]}]}`)
	require.True(t, responsesBodyDeclaresExec(body))
}

func TestOpenAIToolCapabilityFailureRequiresContractAndEvidence(t *testing.T) {
	c, _ := gin.CreateTestContext(nil)
	setOpenAIExecContract(c, []byte(`{"tools":[{"type":"namespace","name":"functions","tools":[{"type":"custom","name":"exec"}]}]}`), false)
	setOpenAIExecContract(c, []byte(`{"tools":[{"type":"function","name":"exec"}]}`), true)
	observeOpenAIToolCapabilitySSE(c, "response.output_text.delta", []byte(`{"delta":"当前会话没有可用的终端工具"}`))
	require.False(t, openAIToolCapabilityFailure(c, false))
	require.True(t, openAIToolCapabilityFailure(c, true))
}

func TestOpenAIToolCapabilityFailureClearsAfterExecCall(t *testing.T) {
	c, _ := gin.CreateTestContext(nil)
	setOpenAIExecContract(c, []byte(`{"tools":[{"type":"custom","name":"exec"}]}`), false)
	setOpenAIExecContract(c, []byte(`{"tools":[{"type":"function","name":"exec"}]}`), true)
	observeOpenAIToolCapabilitySSE(c, "response.output_text.delta", []byte(`{"delta":"no terminal tool is available"}`))
	observeOpenAIToolCapabilitySSE(c, "response.output_item.added", []byte(`{"item":{"type":"custom_tool_call","name":"exec"}}`))
	require.False(t, openAIToolCapabilityFailure(c, true))
}

func TestExplicitExecDenial(t *testing.T) {
	for _, text := range []string{
		"当前会话的工具入口未提供",
		"当前会话没有可用的终端工具",
		"我无法访问文件系统",
		"No terminal tool is available in this session",
		"The exec tool is not exposed",
		"I cannot access the workspace tools",
	} {
		require.True(t, explicitExecDenial(text), text)
	}

	for _, text := range []string{
		"没有必要调用工具",
		"我没有必要访问终端",
		"本次不需要使用工具",
		"I do not have to use a tool for this answer",
		"No tool call is necessary",
		"工具调用已成功完成",
	} {
		require.False(t, explicitExecDenial(text), text)
	}
}

func TestLeakedExecProtocolRequiresStructuredOutput(t *testing.T) {
	require.True(t, leakedExecProtocol("to=functions.exec code:\n{\"cmd\":\"pwd\"}"))
	require.False(t, leakedExecProtocol("I did not modify executor.go"))
	require.False(t, leakedExecProtocol("why there is no terminal output"))
	require.False(t, leakedExecProtocol("to=functions.exec code: not json"))
}

func TestLeakedExecProtocolVariants(t *testing.T) {
	for _, output := range []string{
		"to=functions.exec:\n{\"command\":\"pwd\"}",
		"to=container.exec code:\r\n{\"cmd\":[\"bash\",\"-lc\",\"pwd\"]}",
		"  to=container.exec:\n{\"input\":\"pwd\"}\n\nmore text",
		"to=functions.exec code:\n{\"cmd\":\"pwd\"}\n\nto=functions.exec code:\n{\"cmd\":\"ls\"}",
	} {
		require.True(t, leakedExecProtocol(output), output)
	}
	for _, output := range []string{
		"```text\nto=functions.exec code:\n{\"cmd\":\"pwd\"}\n```",
		"> to=functions.exec code:\n{\"cmd\":\"pwd\"}",
		"    to=functions.exec code:\n{\"cmd\":\"pwd\"}",
		"to=functions.exec code:\n{\"cmd\":42}",
		"to=container.exec:\n{\"cmd\":\"  \"}",
		"to=functions.exec code:\n{\"other\":\"pwd\"}",
		"to=functions.exec code:\nnot json",
	} {
		require.False(t, leakedExecProtocol(output), output)
	}
	require.Equal(t, openAIExecProtocolPending, classifyLeakedExecProtocol("to=fun"))
	require.Equal(t, openAIExecProtocolPending, classifyLeakedExecProtocol("to=functions.exec code:\n{\"cmd\":\"pw"))
}

func TestOpenAIExecProtocolGuardSplitAndOrdinaryOutput(t *testing.T) {
	c, _ := gin.CreateTestContext(nil)
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	setOpenAIExecContract(c, []byte(`{"tools":[{"type":"custom","name":"exec"}]}`), false)
	setOpenAIExecContract(c, []byte(`{"tools":[{"type":"function","name":"exec"}]}`), true)
	guard := newOpenAIExecProtocolGuard(c)
	require.Equal(t, openAIExecProtocolPending, guard.observe("response.output_text.delta", []byte(`{"delta":"to=functions."}`), true, false))
	require.True(t, guard.pending())
	require.Equal(t, openAIExecProtocolPending, guard.observe("response.output_text.delta", []byte(`{"delta":"exec code:\n{\"cmd\":\"pw"}`), true, false))
	require.Equal(t, openAIExecProtocolLeak, guard.observe("response.output_text.delta", []byte(`{"delta":"d\"}"}`), true, false))

	ordinary := newOpenAIExecProtocolGuard(c)
	require.Equal(t, openAIExecProtocolOrdinary, ordinary.observe("response.output_text.delta", []byte(`{"delta":"Ready"}`), true, false))
	require.False(t, ordinary.pending())

	tooLong := newOpenAIExecProtocolGuard(c)
	require.Equal(t, openAIExecProtocolOrdinary, tooLong.observe("response.output_text.delta", []byte(`{"delta":"`+strings.Repeat(" ", openAIExecProtocolPrefixMaxBytes+1)+`"}`), true, false))
}

func TestOpenAIExecProtocolGuardUsesCompletedTextWhenDeltasArePartial(t *testing.T) {
	c, _ := gin.CreateTestContext(nil)
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	setOpenAIExecContract(c, []byte(`{"tools":[{"type":"custom","name":"exec"}]}`), false)
	setOpenAIExecContract(c, []byte(`{"tools":[{"type":"function","name":"exec"}]}`), true)
	guard := newOpenAIExecProtocolGuard(c)
	require.Equal(t, openAIExecProtocolPending, guard.observe("response.output_text.delta", []byte(`{"delta":"to=functions.exec code:\n{\"cmd\":\"pw"}`), true, false))
	require.Equal(t, openAIExecProtocolLeak, guard.observe("response.output_text.done", []byte(`{"text":"to=functions.exec code:\n{\"cmd\":\"pwd\"}"}`), true, false))
	itemGuard := newOpenAIExecProtocolGuard(c)
	require.Equal(t, openAIExecProtocolPending, itemGuard.observe("response.output_text.delta", []byte(`{"delta":"to=functions.exec code:\n{\"cmd\":\"pw"}`), true, false))
	require.Equal(t, openAIExecProtocolLeak, itemGuard.observe("response.output_item.done", []byte(`{"item":{"type":"message","content":[{"type":"output_text","text":"to=functions.exec code:\n{\"cmd\":\"pwd\"}"}]}}`), true, false))
}

func TestOpenAIToolCapabilityIgnoresNonAssistantDenialAndResetsPerAttempt(t *testing.T) {
	c, _ := gin.CreateTestContext(nil)
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	contract := []byte(`{"tools":[{"type":"custom","name":"exec"}]}`)
	setOpenAIExecContract(c, contract, false)
	setOpenAIExecContract(c, contract, true)
	observeOpenAIToolCapabilitySSE(c, "response.reasoning_text.delta", []byte(`{"delta":"no terminal tool is available"}`))
	observeOpenAIToolCapabilitySSE(c, "response.failed", []byte(`{"error":{"message":"no terminal tool is available"}}`))
	require.False(t, openAIToolCapabilityFailure(c, true))
	observeOpenAIToolCapabilitySSE(c, "response.output_text.delta", []byte(`{"delta":"to=container.exec:\n{\"cmd\":[\"pwd\"]}"}`))
	require.True(t, openAIToolCapabilityStrongLeak(c))
	setOpenAIExecContract(c, contract, true)
	require.False(t, openAIToolCapabilityStrongLeak(c))
	require.False(t, openAIToolCapabilityFailure(c, true))
}

func TestOpenAIToolCapabilityFailureAcceptsProtocolLeakOnlyOnOutputText(t *testing.T) {
	c, _ := gin.CreateTestContext(nil)
	setOpenAIExecContract(c, []byte(`{"tools":[{"type":"custom","name":"exec"}]}`), false)
	setOpenAIExecContract(c, []byte(`{"tools":[{"type":"function","name":"exec"}]}`), true)
	observeOpenAIToolCapabilitySSE(c, "response.output_text.delta", []byte(`{"delta":"to=functions.exec code:\n{\"cmd\":\"pwd\"}"}`))
	require.True(t, openAIToolCapabilityFailure(c, true))
}

func TestOpenAIExecProtocolGuardOnlyAppliesToNativeResponses(t *testing.T) {
	contract := []byte(`{"tools":[{"type":"custom","name":"exec"}]}`)
	for _, endpoint := range []string{"/v1/responses", "/openai/v1/responses", "/responses", "/backend-api/codex/responses"} {
		c, _ := gin.CreateTestContext(nil)
		c.Request = httptest.NewRequest("POST", endpoint, nil)
		setOpenAIExecContract(c, contract, false)
		setOpenAIExecContract(c, contract, true)
		require.True(t, newOpenAIExecProtocolGuard(c).enabled, endpoint)
	}
	for _, endpoint := range []string{"/v1/chat/completions", "/v1/messages", "/v1/responses/compact"} {
		c, _ := gin.CreateTestContext(nil)
		c.Request = httptest.NewRequest("POST", endpoint, nil)
		setOpenAIExecContract(c, contract, false)
		setOpenAIExecContract(c, contract, true)
		require.False(t, newOpenAIExecProtocolGuard(c).enabled, endpoint)
	}
}
