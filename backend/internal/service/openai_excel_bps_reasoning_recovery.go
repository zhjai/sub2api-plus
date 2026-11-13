package service

import (
	"bytes"
	"io"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/service/basispoints"
	"github.com/tidwall/gjson"
)

// One exact pre-output rejection may be retried on the same account after
// removing unreadable historical reasoning. No network/EOF/429 retry is added.
func (s *OpenAIGatewayService) recoverExcelBPSEncryptedReasoning(req *http.Request, resp *http.Response, token string, account *Account, proxy string) (*http.Request, *http.Response, error) {
	if resp.StatusCode != http.StatusBadRequest || req.GetBody == nil {
		return req, resp, nil
	}
	raw := s.readUpstreamErrorBody(resp)
	resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(raw))
	if gjson.GetBytes(raw, "error.code").String() != "invalid_encrypted_content" {
		return req, resp, nil
	}
	// A charged rejection is retained for normal accounting rather than hidden
	// behind an internal retry whose usage could not be represented accurately.
	if gjson.GetBytes(raw, "usage").Exists() || gjson.GetBytes(raw, "error.usage").Exists() {
		return req, resp, nil
	}
	body, err := req.GetBody()
	if err != nil {
		return req, resp, nil
	}
	prepared, err := io.ReadAll(body)
	body.Close()
	if err != nil {
		return req, resp, nil
	}
	next, changed, err := basispoints.WithoutEncryptedReasoning(prepared)
	if err != nil || !changed {
		return req, resp, nil
	}
	retry, err := newExcelBPSRequest(req.Context(), next, token, excelOAuthAccountID(token))
	if err != nil {
		return req, resp, nil
	}
	resp.Body.Close()
	fresh, err := s.doAccountRPMUpstream(retry, proxy, account)
	return retry, fresh, err
}
