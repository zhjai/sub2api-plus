package service

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service/basispoints"
	"github.com/gin-gonic/gin"
)

// Compact uses the real upstream compaction trigger. The adapter validates the
// entire returned window; no gateway-generated summary replaces opaque state.
func (s *OpenAIGatewayService) finishExcelBPSCompaction(ctx context.Context, c *gin.Context, account *Account, resp *http.Response, originalModel, billingModel, model string, start time.Time) (*OpenAIForwardResult, error) {
	defer resp.Body.Close()
	result := &OpenAIForwardResult{Model: originalModel, BillingModel: billingModel, UpstreamModel: model,
		UpstreamEndpoint: "/basispoints/api/responses", RequestID: resp.Header.Get("x-request-id"), UpstreamHeaders: resp.Header,
		ResponsesOutcomeObserved: true}
	normalized, measurement := NewExcelBPSBillingReader(account, resp.Body)
	response, err := basispoints.ReadCompaction(normalized, func(event []byte) {
		s.parseSSEUsageBytes(event, &result.Usage)
	})
	result.UpstreamUsage = measurement.UpstreamUsage
	result.Duration = time.Since(start)
	if response != nil {
		if id, ok := response["id"].(string); ok {
			result.ResponseID = id
		}
		if raw, encodeErr := json.Marshal(response); encodeErr == nil {
			if usage, known := extractOpenAIUsageFromJSONBytes(raw); known {
				result.Usage = usage
			}
		}
	}
	if err != nil {
		result.ResponsesProtocolStatus, result.ResponsesStatus = "failed", "failed"
		if !c.Writer.Written() {
			c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"type": "server_error", "code": "basispoints_compaction_failed", "message": err.Error()}})
		}
		return result, err
	}
	result.ResponsesProtocolStatus, result.ResponsesStatus = "completed", "completed"
	response["object"] = "response.compaction"
	c.JSON(http.StatusOK, response)
	s.bindHTTPResponseAccount(ctx, c, account, result.ResponseID)
	return result, nil
}
