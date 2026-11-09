package service

import (
	"fmt"
	"strings"

	openaiidentity "github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/gin-gonic/gin"
)

const (
	openAIDeferInterruptionTerminalKey = "openai_defer_interruption_terminal"
	openAIInterruptionTerminalKey      = "openai_interruption_terminal"
)

// DeferOpenAIInterruptionTerminal keeps a retryable terminal private until the
// handler has persisted the session escape. Otherwise a fast client may retry
// before the failed account has been excluded.
func DeferOpenAIInterruptionTerminal(c *gin.Context) {
	c.Set(openAIDeferInterruptionTerminalKey, true)
}

// FlushOpenAIInterruptionTerminal is called on handler exit, after account health
// and session escape have been recorded. It never starts another upstream call.
func FlushOpenAIInterruptionTerminal(c *gin.Context) {
	value, exists := c.Get(openAIInterruptionTerminalKey)
	if !exists {
		return
	}
	c.Set(openAIInterruptionTerminalKey, nil)
	terminal, _ := value.(string)
	if terminal == "" || c.Request.Context().Err() != nil {
		return
	}
	if _, err := c.Writer.WriteString(terminal); err == nil {
		c.Writer.Flush()
	}
}

func isOpenAICodexStreamClient(c *gin.Context) bool {
	return c != nil && c.Request != nil && openaiidentity.IsCodexOfficialClientByHeaders(
		c.Request.UserAgent(), c.GetHeader("originator"))
}

// A transport failure is not a valid incomplete generation. Codex treats
// incomplete/stream_terminated as fatal, whereas a server_error terminal enters
// its normal client-owned reconnect path. Internal outcome remains premature_eof
// so account health and session escape still reflect the interruption.
func buildOpenAIStreamInterruptionSSE(c *gin.Context, responseID, model string, createdAt, sequenceNumber int64) string {
	if !isOpenAICodexStreamClient(c) {
		return buildOpenAIResponseIncompleteSSE(responseID, model, createdAt, sequenceNumber)
	}
	source := []byte(fmt.Sprintf(`{"sequence_number":%d,"error":{"type":"server_error","code":"server_error","message":"Upstream stream interrupted before completion. Retry the request."}}`, sequenceNumber))
	return buildOpenAIResponseFailedSSE(responseID, model, createdAt, source, "")
}

func deferOpenAIInterruptionTerminal(c *gin.Context, terminal string) bool {
	if !isOpenAICodexStreamClient(c) || !strings.Contains(terminal, "event: response.failed\n") {
		return false
	}
	deferred, _ := c.Get(openAIDeferInterruptionTerminalKey)
	if deferred != true {
		return false
	}
	c.Set(openAIInterruptionTerminalKey, terminal)
	return true
}
