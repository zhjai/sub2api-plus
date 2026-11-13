package service

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/service/basispoints"
	"github.com/Wei-Shaw/sub2api/internal/util/responseheaders"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// OpenAILocalBridgeError is a local adapter preparation or conversion failure. It must not replay the request or penalize provider/account health.
// Usage returned alongside this error still belongs to the completed attempt.
type OpenAILocalBridgeError struct {
	Failure *basispoints.BridgeFailure
}

func (e *OpenAILocalBridgeError) Error() string {
	return "local BPS bridge failure: " + e.Failure.Message
}

func parseOpenAILocalBridgeError(payload []byte) *OpenAILocalBridgeError {
	if failure := basispoints.ParseBridgeFailure(payload); failure != nil {
		return &OpenAILocalBridgeError{Failure: failure}
	}
	return nil
}

func openAIResponseFailedError(message string, local *OpenAILocalBridgeError) error {
	if local != nil {
		return local
	}
	return fmt.Errorf("upstream response failed: %s", message)
}

// Retain the failed response object, including identity, known usage and
// structural diagnostics. Reconstructing successful output from prior deltas
// here would release tools the BPS bridge intentionally withheld.
func (s *OpenAIGatewayService) writeOpenAINonStreamingTerminalFailure(resp *http.Response, c *gin.Context, account *Account, payload []byte, message string) error {
	local := parseOpenAILocalBridgeError(payload)
	status := http.StatusBadGateway
	if account != nil && account.IsExcelOAuth() {
		status = openAIStreamFailedEventSemanticStatus(payload, message)
	}
	if local != nil {
		status = http.StatusBadGateway
	}
	// Only BPS has normalized failure payloads. Other upstreams may include
	// credentials or private diagnostics in arbitrary fields; preserve the
	// existing sanitized public error contract for those accounts.
	body, _ := json.Marshal(gin.H{"error": gin.H{"type": "upstream_error", "message": sanitizeUpstreamErrorMessage(message)}})
	if account != nil && account.IsExcelOAuth() {
		body = payload
		if response := gjson.GetBytes(payload, "response"); response.IsObject() {
			body = []byte(response.Raw)
		}
	}
	s.recordOpenAIStreamUpstreamError(c, account, false, resp.Header.Get("x-request-id"), "terminal_failure", payload, message)
	responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	if !writeOpenAICompactSSEBridge(c, status, body) {
		c.Header("Content-Type", "application/json; charset=utf-8")
		c.Data(status, "application/json; charset=utf-8", body)
	}
	return openAIResponseFailedError(message, local)
}

func openAIStreamExplicitServerFailure(payload []byte) bool {
	for _, path := range openAIStreamErrorStatusPaths {
		if status := gjson.GetBytes(payload, path).Int(); status >= 500 && status <= 599 {
			return true
		}
	}
	return false
}
