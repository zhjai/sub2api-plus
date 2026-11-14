package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service/basispoints"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// ResolveOpenAIImagesAccountRoute is a pure pre-send gate. Unsupported BPS
// parameters keep their complete semantics on a native grant or another account.
// The scheduler's account ID, concurrency slot and RPM accounting stay unchanged.
func ResolveOpenAIImagesAccountRoute(account *Account, parsed *OpenAIImagesRequest, channelModel string) (*Account, error) {
	if account == nil || parsed == nil {
		return nil, fmt.Errorf("missing image account or request")
	}
	model := strings.TrimSpace(channelModel)
	if model == "" {
		model = strings.TrimSpace(parsed.Model)
	}
	if model == "" {
		model = "gpt-image-2"
	}
	bps := account.BPSAccountForModel(model)
	if bps == nil && !account.IsExcelOAuth() {
		return account, nil
	}
	if bps != nil && excelBPSImagesSupportedModel(bps.GetMappedModel(model)) && excelBPSImagesUnsupportedReason(parsed) == "" {
		return bps, nil
	}
	if native := ExcelBPSNativeFallbackAccount(account); native != nil {
		return native, nil
	}
	return nil, fmt.Errorf("BPS image route cannot preserve the requested model or image parameters")
}

// forwardExcelBPSImages never turns an upstream error into a capability retry.
// Once dispatched, this image request has a single owner, including failures.
func (s *OpenAIGatewayService) forwardExcelBPSImages(ctx context.Context, c *gin.Context, account *Account, parsed *OpenAIImagesRequest, channelModel string) (_ *OpenAIForwardResult, forwardErr error) {
	start := time.Now()
	model := strings.TrimSpace(channelModel)
	if model == "" {
		model = strings.TrimSpace(parsed.Model)
	}
	if model == "" {
		model = "gpt-image-2"
	}
	upstreamModel := account.GetMappedModel(model)
	if !excelBPSImagesSupportedModel(upstreamModel) || excelBPSImagesUnsupportedReason(parsed) != "" {
		return nil, fmt.Errorf("unsupported BPS image request before dispatch")
	}
	var body []byte
	var err error
	contentType, endpoint, target := "application/json", excelBPSImagesEndpoint, basispoints.ImagesGenerationsURL
	if parsed.IsEdits() {
		body, contentType, err = buildExcelBPSImagesEditsBody(parsed, upstreamModel)
		endpoint, target = excelBPSImagesEditsEndpoint, basispoints.ImagesEditsURL
	} else {
		body, err = buildExcelBPSImagesPayload(parsed, upstreamModel)
	}
	if err != nil {
		return nil, err
	}
	account, lease, err := s.acquireExcelBPSSessionProxy(ctx, c, account, body)
	if err != nil {
		return nil, err
	}
	var leaseFailure error
	if lease != nil {
		defer func() {
			if leaseFailure != nil {
				lease.release(leaseFailure)
			} else {
				lease.release(forwardErr)
			}
		}()
	}
	token, _, err := s.GetAccessToken(ctx, account)
	if err != nil {
		return nil, err
	}
	if err = validateExcelOAuthAccount(account, token); err != nil {
		return nil, err
	}
	req, err := newExcelBPSImagesRequest(WithHTTPUpstreamSingleSend(ctx), target, contentType, body, token, excelOAuthAccountID(token))
	if err != nil {
		return nil, err
	}
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	beginUpstreamResponseModelObservation(c)
	SetOpsUpstreamModel(c, upstreamModel)
	SetActualOpenAIUpstreamEndpoint(c, endpoint)
	sent := time.Now()
	resp, err := s.doAccountRPMUpstream(req, proxyURL, account)
	SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(sent).Milliseconds())
	fail := func(status int, code, message string) (*OpenAIForwardResult, error) {
		upErr := &OpenAIImagesUpstreamError{StatusCode: status, ErrorType: "upstream_error", Code: code, Message: message}
		writeOpenAIImagesUpstreamErrorResponse(c, upErr)
		return nil, upErr
	}
	if err != nil {
		if IsAccountRPMError(err) || ctx.Err() != nil {
			return nil, err
		}
		leaseFailure = err
		return fail(http.StatusBadGateway, "basispoints_transport_error", "BPS image connection failed; request was not replayed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw := redactExcelBPSCredentialBody(s.readUpstreamErrorBody(resp), account, token)
		s.handleOpenAIAccountUpstreamError(ctx, account, resp.StatusCode, resp.Header, raw, upstreamModel)
		return fail(resp.StatusCode, "basispoints_upstream_error", fmt.Sprintf("BPS image endpoint returned HTTP %d; request was not replayed", resp.StatusCode))
	}
	raw, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		leaseFailure = err
		return fail(http.StatusBadGateway, "basispoints_image_response_invalid", "BPS image response was incomplete; request was not replayed")
	}
	measurement := &ExcelBPSBillingMeasurement{}
	originalUsage, usageKnown := codexDirectImagesUsage(raw)
	normalized := NormalizeExcelBPSUsageJSON(account, raw, measurement)
	if usageKnown {
		measurement.UpstreamUsage = &originalUsage
	}
	converted := *resp
	converted.Body = io.NopCloser(bytes.NewReader(normalized))
	usage, count, sizes, err := s.handleCodexDirectImagesNonStreamingResponse(&converted, c, parsed)
	if err != nil {
		return fail(http.StatusBadGateway, "basispoints_image_response_invalid", "BPS image response was incomplete or invalid; request was not replayed")
	}
	return &OpenAIForwardResult{Model: model, UpstreamModel: upstreamModel, UpstreamEndpoint: endpoint,
		RequestID: resp.Header.Get("x-request-id"), Usage: usage, UpstreamUsage: measurement.UpstreamUsage, Duration: time.Since(start),
		UpstreamHeaders: resp.Header, ResponseHeaders: resp.Header.Clone(), ImageCount: count,
		ImageSize: parsed.SizeTier, ImageInputSize: parsed.Size, ImageOutputSizes: sizes,
		UpstreamResponseModel: observedUpstreamResponseModel(c), UpstreamResponseModelConflict: observedUpstreamResponseModelConflict(c)}, nil
}

const (
	excelBPSImagesEndpoint      = "/basispoints/api/images/generations"
	excelBPSImagesEditsEndpoint = "/basispoints/api/images/edits"
)

// The allowlist follows the public ranxi image adapter's reported support.
// It is deliberately narrow; this port does not claim live upstream validation.
func excelBPSImagesSupportedModel(model string) bool {
	return strings.TrimSpace(model) == "gpt-image-2"
}

// excelBPSImagesUnsupportedReason reports why a request must stay on Codex.
// The public reference reports png output and opaque/auto backgrounds. Preserve
// all other requested semantics through a native route before sending anything.
func excelBPSImagesUnsupportedReason(parsed *OpenAIImagesRequest) string {
	switch {
	case parsed == nil:
		return "missing request"
	case parsed.Stream:
		return "stream"
	case parsed.PartialImages != nil:
		return "partial_images"
	case parsed.OutputCompression != nil:
		return "output_compression"
	case strings.TrimSpace(parsed.Style) != "":
		return "style"
	case strings.TrimSpace(parsed.InputFidelity) != "":
		return "input_fidelity"
	case parsed.HasMask || parsed.MaskUpload != nil || strings.TrimSpace(parsed.MaskImageURL) != "":
		return "mask"
	}
	switch strings.ToLower(strings.TrimSpace(parsed.OutputFormat)) {
	case "", "png":
	default:
		return "output_format"
	}
	switch strings.ToLower(strings.TrimSpace(parsed.Moderation)) {
	case "", "auto":
	default:
		return "moderation"
	}
	switch strings.ToLower(strings.TrimSpace(parsed.Background)) {
	case "", "opaque", "auto":
	default:
		return "background"
	}
	if parsed.IsEdits() {
		return excelBPSImagesEditsUnsupportedReason(parsed)
	}
	return ""
}

// The BPS edit endpoint requires exactly one image file; masks and remote
// image URLs (which the gateway would have to download) stay on Codex.
func excelBPSImagesEditsUnsupportedReason(parsed *OpenAIImagesRequest) string {
	if parsed.HasMask || parsed.MaskUpload != nil || strings.TrimSpace(parsed.MaskImageURL) != "" {
		return "mask"
	}
	images := len(parsed.Uploads)
	for _, imageURL := range parsed.InputImageURLs {
		if imageURL = strings.TrimSpace(imageURL); imageURL == "" {
			continue
		}
		images++
		if _, _, err := decodeAccountTestDataURL(imageURL); err != nil {
			return "remote image_url"
		}
	}
	if images != 1 {
		return "image count"
	}
	return ""
}

// buildExcelBPSImagesPayload reuses the Codex generations body. moderation=auto
// is the upstream default, and BPS rejects the field itself.
func buildExcelBPSImagesPayload(parsed *OpenAIImagesRequest, model string) ([]byte, error) {
	body, _, err := buildOpenAIImagesOAuthPayload(parsed, model)
	if err != nil {
		return nil, err
	}
	return sjson.DeleteBytes(body, "moderation")
}

var excelBPSImagesQuoteEscaper = strings.NewReplacer("\\", "\\\\", `"`, `\"`)

// buildExcelBPSImagesEditsBody builds the multipart edit request. Unlike the
// Codex edit endpoint, which takes JSON data URLs, BPS wants one file part
// plus the same form fields the generations endpoint accepts.
func buildExcelBPSImagesEditsBody(parsed *OpenAIImagesRequest, model string) ([]byte, string, error) {
	prompt := parsed.Prompt
	if !parsed.Multipart && gjson.ValidBytes(parsed.Body) {
		if rawPrompt := gjson.GetBytes(parsed.Body, "prompt").String(); rawPrompt != "" {
			prompt = rawPrompt
		}
	}
	if strings.TrimSpace(prompt) == "" {
		return nil, "", fmt.Errorf("prompt is required")
	}
	fileName, contentType, data := "", "", []byte(nil)
	if len(parsed.Uploads) > 0 {
		upload := parsed.Uploads[0]
		fileName, contentType, data = upload.FileName, upload.ContentType, upload.Data
	} else {
		for _, imageURL := range parsed.InputImageURLs {
			if imageURL = strings.TrimSpace(imageURL); imageURL == "" {
				continue
			}
			decoded, mimeType, err := decodeAccountTestDataURL(imageURL)
			if err != nil {
				return nil, "", err
			}
			contentType, data = mimeType, decoded
			break
		}
	}
	if len(data) == 0 {
		return nil, "", fmt.Errorf("image input is required")
	}
	mediaType, _, _ := mime.ParseMediaType(contentType)
	// Standard multipart writers label file parts application/octet-stream.
	// BPS requires the actual image MIME even when the file bytes are valid.
	if strings.TrimSpace(contentType) == "" || strings.EqualFold(mediaType, "application/octet-stream") {
		contentType = http.DetectContentType(data)
	}
	if strings.TrimSpace(fileName) == "" {
		// BPS reads the part's Content-Type; the name is only cosmetic.
		fileName = "image.png"
	}
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	fields := [][2]string{{"model", model}, {"prompt", prompt},
		{"size", strings.TrimSpace(parsed.Size)}, {"quality", strings.TrimSpace(parsed.Quality)},
		{"background", strings.TrimSpace(parsed.Background)}, {"output_format", strings.TrimSpace(parsed.OutputFormat)}}
	if parsed.N > 1 {
		fields = append(fields, [2]string{"n", strconv.Itoa(parsed.N)})
	}
	for _, field := range fields {
		if field[1] == "" {
			continue
		}
		if err := writer.WriteField(field[0], field[1]); err != nil {
			return nil, "", err
		}
	}
	// CreateFormFile would hardcode application/octet-stream; keep the image MIME.
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="image"; filename="`+excelBPSImagesQuoteEscaper.Replace(fileName)+`"`)
	header.Set("Content-Type", contentType)
	part, err := writer.CreatePart(header)
	if err != nil {
		return nil, "", err
	}
	if _, err := part.Write(data); err != nil {
		return nil, "", err
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), writer.FormDataContentType(), nil
}

// The images endpoint checks the full Excel client identity, not only the
// product and agent profile that the Responses endpoint needs.
func newExcelBPSImagesRequest(ctx context.Context, upstreamURL, contentType string, body []byte, token, accountID string) (*http.Request, error) {
	req, err := newExcelBPSRequest(ctx, body, token, accountID)
	if err != nil {
		return nil, err
	}
	target, err := url.Parse(upstreamURL)
	if err != nil {
		return nil, err
	}
	req.URL = target
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for key, value := range map[string]string{
		"X-Openai-Internal-Basispoints-Client-Platform":       "excel",
		"X-Openai-Internal-Basispoints-Client-Editor":         "excel",
		"X-Openai-Internal-Basispoints-Client-Host":           "office",
		"X-Openai-Internal-Basispoints-Client-Runtime":        "desktop",
		"X-Openai-Internal-Basispoints-Client-Platform-Class": "PC",
		"X-Openai-Internal-Basispoints-Office-Host":           "Excel",
		"X-Openai-Internal-Basispoints-Office-Platform":       "PC",
	} {
		req.Header.Set(key, value)
	}
	return req, nil
}
