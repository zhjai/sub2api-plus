package service

import (
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

func TestOpenAIToolCapabilityFailureAcceptsProtocolLeakOnlyOnOutputText(t *testing.T) {
	c, _ := gin.CreateTestContext(nil)
	setOpenAIExecContract(c, []byte(`{"tools":[{"type":"custom","name":"exec"}]}`), false)
	setOpenAIExecContract(c, []byte(`{"tools":[{"type":"function","name":"exec"}]}`), true)
	observeOpenAIToolCapabilitySSE(c, "response.output_text.delta", []byte(`{"delta":"to=functions.exec code:\n{\"cmd\":\"pwd\"}"}`))
	require.True(t, openAIToolCapabilityFailure(c, true))
}
