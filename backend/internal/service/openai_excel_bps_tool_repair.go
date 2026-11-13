package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service/basispoints"
	"github.com/tidwall/gjson"
)

// Only the bridge can request a correction, after a complete terminal tool
// batch has failed transport validation and before any client tool is emitted.
// It enforces two corrections at most and checks original operation bytes.
// This callback never switches credentials, route, model, effort or proxy.
func (s *OpenAIGatewayService) excelBPSToolRepair(original *http.Request, token string, account *Account, proxy string) basispoints.ToolRepairFunc {
	if original.GetBody == nil {
		return nil
	}
	body, err := original.GetBody()
	if err != nil {
		return nil
	}
	prepared, err := io.ReadAll(body)
	_ = body.Close()
	if err != nil {
		return nil
	}
	requestedModel := gjson.GetBytes(prepared, "model").String()
	var initial map[string]any
	return func(ctx context.Context, failed map[string]any, validation error) (map[string]any, error) {
		if initial == nil {
			initial = failed
		}
		mismatch := func(response map[string]any) error {
			declared, _ := response["model"].(string)
			if !openAIPreCommitObservedResponseModelMismatch(requestedModel, declared) {
				return nil
			}
			// Keep the mismatched declaration visible to the normal gateway
			// observer. Otherwise the bridge's original response identity
			// would mask a later correction from a different model.
			initial["model"] = normalizeObservedUpstreamResponseModel(declared)
			return &basispoints.UpstreamFailure{Status: http.StatusBadGateway, Code: "response_model_mismatch", Type: "server_error", Message: "BPS tool correction returned a different model"}
		}
		if err := mismatch(failed); err != nil {
			return nil, err
		}
		next, err := basispoints.BuildToolRepairRequest(prepared, failed, validation)
		if err != nil {
			return nil, err
		}
		req, err := newExcelBPSRequest(ctx, next, token, excelOAuthAccountID(token))
		if err != nil {
			return nil, fmt.Errorf("BPS tool correction request could not be prepared")
		}
		resp, err := s.doAccountRPMUpstream(req, proxy, account)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if IsAccountRPMError(err) {
				return nil, fmt.Errorf("BPS tool correction send admission unavailable")
			}
			// A provider timeout is not cancellation of the client request.
			// Preserve the completed first turn's usage without exposing proxy
			// addresses or raw transport errors in a bridge failure.
			return nil, &basispoints.UpstreamFailure{Status: http.StatusBadGateway, Code: "basispoints_upstream_error", Type: "server_error", Message: "BPS tool correction transport failed"}
		}
		defer resp.Body.Close()
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			// Preserve structural provider status without exposing its raw body.
			// Gateway retry policy still respects downstream response commitment.
			raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			var failure map[string]any
			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.UseNumber()
			_ = decoder.Decode(&failure)
			if _, ok := failure["usage"]; !ok {
				if detail, ok := failure["error"].(map[string]any); ok {
					if usage, ok := detail["usage"]; ok {
						failure["usage"] = usage
					}
				}
			}
			// Return known usage even when a correction was rejected. The
			// bridge adds it to the original terminal before reporting failure.
			status := resp.StatusCode
			errorType := "server_error"
			if status == http.StatusTooManyRequests {
				errorType = "rate_limit_error"
			} else if status < 500 {
				errorType = "invalid_request_error"
			}
			// Use a typed provider failure so real 429/5xx remain provider
			// evidence instead of being mislabelled as local conversion.
			return failure, &basispoints.UpstreamFailure{Status: status, Code: "basispoints_upstream_error", Type: errorType, Message: fmt.Sprintf("BPS tool transport correction returned HTTP %d", status)}
		}
		prepared = next
		var observedMismatch error
		corrected, err := basispoints.ReadToolRepairResponse(resp.Body, func(model string) {
			if observedMismatch == nil {
				observedMismatch = mismatch(map[string]any{"model": model})
			}
		})
		if ctx.Err() != nil {
			return corrected, ctx.Err()
		}
		if observedMismatch != nil {
			return corrected, observedMismatch
		}
		if modelErr := mismatch(corrected); modelErr != nil {
			return corrected, modelErr
		}
		// The model field can be absent in a correction. Never synthesize a
		// declaration or infer entitlement from the requested model name.
		if declared, ok := corrected["model"].(string); ok && strings.TrimSpace(declared) != "" {
			initial["model"] = declared
		}
		return corrected, err
	}
}
