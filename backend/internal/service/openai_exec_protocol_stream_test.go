package service

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func openAIExecProtocolTestDelta(text string) string {
	return `data: {"type":"response.output_text.delta","delta":` + strconv.Quote(text) + "}\n\n"
}

func TestOpenAINativeExecProtocolLeakSwitchesBeforeCommit(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	contract := []byte(`{"tools":[{"type":"custom","name":"exec"}]}`)
	setOpenAIExecContract(c, contract, false)
	setOpenAIExecContract(c, contract, true)
	svc := &OpenAIGatewayService{}
	badStream := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_bad"}}`,
		"",
		`data: {"type":"response.output_item.added","item":{"type":"message","content":[]}}`,
		"",
		openAIExecProtocolTestDelta("to=functions."),
		openAIExecProtocolTestDelta("exec code:\n{\"cmd\":\"pw"),
		openAIExecProtocolTestDelta("d\"}"),
	}, "\n")
	badResp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(badStream))}
	badResult, err := svc.handleStreamingResponse(c.Request.Context(), badResp, c, &Account{ID: 1, Platform: PlatformOpenAI}, time.Now(), "gpt-test", "gpt-test")
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, OpenAIExecProtocolLeakReason, failoverErr.Reason)
	require.True(t, failoverErr.SafeToFailoverAfterWrite)
	require.True(t, failoverErr.SessionAccountEscape)
	require.True(t, badResult.toolCapabilityFailure)
	require.Empty(t, rec.Body.String())

	setOpenAIExecContract(c, contract, true)
	goodStream := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_good"}}`,
		"",
		`data: {"type":"response.output_item.added","item":{"type":"function_call","name":"exec","call_id":"call_1","arguments":""}}`,
		"",
		`data: {"type":"response.function_call_arguments.delta","delta":"{\"cmd\":\"pwd\"}"}`,
		"",
		`data: {"type":"response.completed","response":{"id":"resp_good","status":"completed","usage":{"input_tokens":1,"output_tokens":1}}}`,
		"",
	}, "\n")
	goodResp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(goodStream))}
	goodResult, err := svc.handleStreamingResponse(c.Request.Context(), goodResp, c, &Account{ID: 2, Platform: PlatformOpenAI}, time.Now(), "gpt-test", "gpt-test")
	require.NoError(t, err)
	require.True(t, goodResult.execCallObserved)
	require.False(t, goodResult.toolCapabilityFailure)
	require.Contains(t, rec.Body.String(), "resp_good")
	require.NotContains(t, rec.Body.String(), "resp_bad")
	require.NotContains(t, rec.Body.String(), "to=functions.exec")
}

type openAIExecFlushRecorder struct {
	*httptest.ResponseRecorder
	flushed chan struct{}
}

func (r *openAIExecFlushRecorder) Flush() {
	r.ResponseRecorder.Flush()
	select {
	case r.flushed <- struct{}{}:
	default:
	}
}

func TestOpenAINativeOrdinaryTextFlushesBeforeTerminal(t *testing.T) {
	rec := &openAIExecFlushRecorder{ResponseRecorder: httptest.NewRecorder(), flushed: make(chan struct{}, 1)}
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	contract := []byte(`{"tools":[{"type":"custom","name":"exec"}]}`)
	setOpenAIExecContract(c, contract, false)
	setOpenAIExecContract(c, contract, true)
	reader, writer := io.Pipe()
	defer writer.Close()
	done := make(chan error, 1)
	go func() {
		resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: reader}
		_, err := (&OpenAIGatewayService{}).handleStreamingResponse(c.Request.Context(), resp, c, &Account{ID: 1, Platform: PlatformOpenAI}, time.Now(), "gpt-test", "gpt-test")
		done <- err
	}()
	_, err := io.WriteString(writer, openAIExecProtocolTestDelta("Ready"))
	require.NoError(t, err)
	select {
	case <-rec.flushed:
		require.Contains(t, rec.Body.String(), "Ready")
	case <-time.After(2 * time.Second):
		t.Fatal("ordinary text was not flushed before the upstream terminal")
	}
	_, err = io.WriteString(writer, `data: {"type":"response.completed","response":{"id":"resp_ok","status":"completed"}}`+"\n\n")
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	require.NoError(t, <-done)
}

func TestOpenAINativePendingExecPrefixIdlesWithoutLeaking(t *testing.T) {
	cfg := &config.Config{Gateway: config.GatewayConfig{StreamDataIntervalTimeout: 1}}
	svc := &OpenAIGatewayService{cfg: cfg}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	contract := []byte(`{"tools":[{"type":"custom","name":"exec"}]}`)
	setOpenAIExecContract(c, contract, false)
	setOpenAIExecContract(c, contract, true)
	reader, writer := io.Pipe()
	defer writer.Close()
	go func() {
		_, _ = io.WriteString(writer, openAIExecProtocolTestDelta("to=functions."))
	}()
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: reader}
	_, err := svc.handleStreamingResponse(c.Request.Context(), resp, c, &Account{ID: 1, Platform: PlatformOpenAI}, time.Now(), "gpt-test", "gpt-test")
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.True(t, failoverErr.SessionAccountEscape)
	require.Empty(t, rec.Body.String())
}

func TestOpenAINativeCommittedTextNeverSwitchesOnLaterRawEnvelope(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	contract := []byte(`{"tools":[{"type":"custom","name":"exec"}]}`)
	setOpenAIExecContract(c, contract, false)
	setOpenAIExecContract(c, contract, true)
	stream := openAIExecProtocolTestDelta("Ready\n") +
		openAIExecProtocolTestDelta("to=functions.exec code:\n{\"cmd\":\"pwd\"}") +
		`data: {"type":"response.completed","response":{"id":"resp_done","status":"completed"}}` + "\n\n"
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(stream))}
	result, err := (&OpenAIGatewayService{}).handleStreamingResponse(c.Request.Context(), resp, c, &Account{ID: 1, Platform: PlatformOpenAI}, time.Now(), "gpt-test", "gpt-test")
	require.NoError(t, err)
	require.Contains(t, rec.Body.String(), "Ready")
	require.Contains(t, rec.Body.String(), "to=functions.exec")
	require.True(t, result.toolCapabilityFailure, "retain the confirmed protocol leak after output is committed")
	forwardResult := &OpenAIForwardResult{
		ResponsesOutcomeObserved:   result.responsesOutcomeObserved,
		ResponsesProtocolStatus:    result.responsesProtocolStatus,
		ResponsesIncompleteReason:  result.responsesIncompleteReason,
		ResponsesMeaningfulOutput:  result.responsesMeaningfulOutput,
		ResponsesToolCallForwarded: result.responsesToolCallForwarded,
		ToolCapabilityFailure:      result.toolCapabilityFailure,
		UpstreamTerminalEvent:      result.responsesTerminalEvent,
	}
	require.True(t, forwardResult.RequiresSessionAccountEscape(), "the next sampling request should escape this account")
}
