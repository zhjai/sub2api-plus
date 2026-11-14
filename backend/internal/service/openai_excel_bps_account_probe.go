package service

import (
	"context"
	"fmt"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/service/basispoints"
	"github.com/gin-gonic/gin"
)

// model is already account-mapped by testOpenAIAccountConnection. The test
// shares the live BPS payload builder and response parser, without native tool
// substitution or retrying image generation after a send.
func (s *AccountTestService) testExcelBPSImageConnection(c *gin.Context, ctx context.Context, a *Account, model, prompt string) error {
	parsed := &OpenAIImagesRequest{Endpoint: openAIImagesGenerationsEndpoint, Model: model, Prompt: prompt}
	applyOpenAIImagesDefaults(parsed)
	if !excelBPSImagesSupportedModel(model) || excelBPSImagesUnsupportedReason(parsed) != "" {
		if native := ExcelBPSNativeFallbackAccount(a); native != nil {
			return s.testOpenAIImageOAuth(c, ctx, native, model, prompt)
		}
		return s.sendErrorAndEnd(c, "BPS image test cannot preserve the selected model or image parameters")
	}
	body, err := buildExcelBPSImagesPayload(parsed, model)
	if err != nil {
		return s.sendErrorAndEnd(c, err.Error())
	}
	token := a.GetOpenAIAccessToken()
	if err = validateExcelOAuthAccount(a, token); err != nil {
		return s.sendErrorAndEnd(c, "BPS image test grant is unavailable")
	}
	req, err := newExcelBPSImagesRequest(WithHTTPUpstreamSingleSend(ctx), basispoints.ImagesGenerationsURL, "application/json", body, token, excelOAuthAccountID(token))
	if err != nil {
		return s.sendErrorAndEnd(c, "Failed to prepare BPS image test")
	}
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.Flush()
	s.sendEvent(c, TestEvent{Type: "test_start", Model: model})
	proxy := ""
	if a.Proxy != nil {
		proxy = a.Proxy.URL()
	}
	resp, err := s.doAccountTestUpstream(req, proxy, a, nil)
	if err != nil {
		return s.sendErrorAndEnd(c, "BPS image connection failed")
	}
	if resp == nil || resp.Body == nil {
		return s.sendErrorAndEnd(c, "BPS image response is missing")
	}
	defer resp.Body.Close()
	raw, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		return s.sendErrorAndEnd(c, "BPS image response was incomplete")
	}
	if resp.StatusCode != http.StatusOK {
		raw = redactExcelBPSCredentialBody(raw, a, token)
		if s.openaiGatewayService != nil {
			s.openaiGatewayService.handleOpenAIAccountUpstreamError(ctx, a, resp.StatusCode, resp.Header, raw, model)
		}
		return s.sendErrorAndEnd(c, fmt.Sprintf("BPS image API returned %d", resp.StatusCode))
	}
	images, err := parseCodexDirectImagesResponse(raw)
	if err != nil || len(images) == 0 {
		return s.sendErrorAndEnd(c, "BPS image response did not contain a valid image")
	}
	for _, item := range images {
		if item.RevisedPrompt != "" {
			s.sendEvent(c, TestEvent{Type: "content", Text: item.RevisedPrompt})
		}
		mimeType := openAIImageOutputMIMEType(item.OutputFormat)
		s.sendEvent(c, TestEvent{Type: "image", ImageURL: "data:" + mimeType + ";base64," + item.Result, MimeType: mimeType})
	}
	s.sendEvent(c, TestEvent{Type: "test_complete", Success: true})
	return nil
}
