package service

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestExcelBPSImagesPreflightPreservesParameters(t *testing.T) {
	base := &OpenAIImagesRequest{Model: "gpt-image-2", Prompt: "draw", N: 1, OutputFormat: "png", Background: "opaque"}
	bps := excelBPSAccount(t)
	got, err := ResolveOpenAIImagesAccountRoute(bps, base, "")
	require.NoError(t, err)
	require.True(t, got.IsExcelOAuth())
	native := directImagesTestAccount()
	for name, modify := range map[string]func(*OpenAIImagesRequest){
		"stream":      func(p *OpenAIImagesRequest) { p.Stream = true },
		"mask":        func(p *OpenAIImagesRequest) { p.Endpoint = "/v1/images/edits"; p.HasMask = true },
		"transparent": func(p *OpenAIImagesRequest) { p.Background = "transparent" },
		"format":      func(p *OpenAIImagesRequest) { p.OutputFormat = "webp" },
		"partial":     func(p *OpenAIImagesRequest) { n := 0; p.PartialImages = &n },
		"multi": func(p *OpenAIImagesRequest) {
			p.Endpoint = "/v1/images/edits"
			p.InputImageURLs = []string{"data:image/png;base64,YQ==", "data:image/png;base64,Yg=="}
		},
		"remote": func(p *OpenAIImagesRequest) {
			p.Endpoint = "/v1/images/edits"
			p.InputImageURLs = []string{"https://example.org/a.png"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			p := *base
			modify(&p)
			_, err := ResolveOpenAIImagesAccountRoute(bps, &p, "")
			require.Error(t, err)
			got, err := ResolveOpenAIImagesAccountRoute(native, &p, "")
			require.NoError(t, err)
			require.Equal(t, native, got)
		})
	}
	// Ordinary API keys and credential-inheriting shadows remain eligible.
	for _, a := range []*Account{{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, {Platform: PlatformOpenAI, Type: AccountTypeOAuth}} {
		got, err := ResolveOpenAIImagesAccountRoute(a, base, "")
		require.NoError(t, err)
		require.Same(t, a, got)
	}
}

func TestExcelBPSImagesPayloadAndEditPreserveSemantics(t *testing.T) {
	p := &OpenAIImagesRequest{Model: "gpt-image-2", Prompt: "draw", Size: "1536x1024", Quality: "high", N: 2, Background: "opaque", OutputFormat: "png", Moderation: "auto"}
	body, err := buildExcelBPSImagesPayload(p, p.Model)
	require.NoError(t, err)
	require.Equal(t, "1536x1024", gjson.GetBytes(body, "size").String())
	require.EqualValues(t, 2, gjson.GetBytes(body, "n").Int())
	require.False(t, gjson.GetBytes(body, "moderation").Exists())
	p.Endpoint = "/v1/images/edits"
	p.InputImageURLs = []string{"data:image/png;base64,aW1hZ2U="}
	body, ct, err := buildExcelBPSImagesEditsBody(p, p.Model)
	require.NoError(t, err)
	_, params, err := mime.ParseMediaType(ct)
	require.NoError(t, err)
	form, err := multipart.NewReader(bytes.NewReader(body), params["boundary"]).ReadForm(1 << 20)
	require.NoError(t, err)
	defer form.RemoveAll()
	require.Equal(t, []string{"2"}, form.Value["n"])
	require.Equal(t, []string{"1536x1024"}, form.Value["size"])
	require.Len(t, form.File["image"], 1)
	file, err := form.File["image"][0].Open()
	require.NoError(t, err)
	defer file.Close()
	data, err := io.ReadAll(file)
	require.NoError(t, err)
	require.Equal(t, "image", string(data))
	require.Equal(t, "image/png", form.File["image"][0].Header.Get("Content-Type"))
}

func TestExcelBPSImagesEditSniffsGenericMultipartImageMIME(t *testing.T) {
	var imageBytes bytes.Buffer
	require.NoError(t, png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 2, 2))))
	for _, contentType := range []string{"", "application/octet-stream", "application/octet-stream; charset=binary", "image/png"} {
		t.Run(contentType, func(t *testing.T) {
			parsed := &OpenAIImagesRequest{
				Endpoint: "/v1/images/edits", Model: "gpt-image-2", Prompt: "change the circle color",
				Uploads: []OpenAIImagesUpload{{FileName: "source.png", ContentType: contentType, Data: imageBytes.Bytes()}},
			}
			body, ct, err := buildExcelBPSImagesEditsBody(parsed, parsed.Model)
			require.NoError(t, err)
			_, params, err := mime.ParseMediaType(ct)
			require.NoError(t, err)
			form, err := multipart.NewReader(bytes.NewReader(body), params["boundary"]).ReadForm(1 << 20)
			require.NoError(t, err)
			defer form.RemoveAll()
			require.Len(t, form.File["image"], 1)
			require.Equal(t, "image/png", form.File["image"][0].Header.Get("Content-Type"))
			file, err := form.File["image"][0].Open()
			require.NoError(t, err)
			defer file.Close()
			actual, err := io.ReadAll(file)
			require.NoError(t, err)
			require.Equal(t, imageBytes.Bytes(), actual)
		})
	}
}

func TestExcelBPSImagesDualGrantMappedRouteAndNativeFallback(t *testing.T) {
	a := directImagesTestAccount()
	a.Credentials[ExcelBPSCredentialsKey] = excelBPSAccount(t).Credentials
	a.Credentials["model_mapping"] = map[string]any{"image-alias": "gpt-image-2"}
	a.Extra = map[string]any{ExcelBPSEnabledKey: true, "openai_excel_bps_models": []any{"gpt-image-2"}}
	p := &OpenAIImagesRequest{Model: "image-alias", Prompt: "draw", N: 1}
	got, err := ResolveOpenAIImagesAccountRoute(a, p, "")
	require.NoError(t, err)
	require.True(t, got.IsExcelOAuth())
	require.Equal(t, "gpt-image-2", got.GetMappedModel("image-alias"))
	p.Background = "transparent"
	native, err := ResolveOpenAIImagesAccountRoute(a, p, "")
	require.NoError(t, err)
	require.False(t, native.IsExcelOAuth())
	require.Equal(t, a.GetOpenAIAccessToken(), native.GetOpenAIAccessToken())
	require.Nil(t, native.BPSAccountForModel("image-alias"), "native fallback must not switch back to BPS in ForwardImages")
	require.Equal(t, true, a.Extra[ExcelBPSEnabledKey], "routing must not mutate saved configuration")
}

func TestExcelBPSImagesSingleDispatchSuccessAndFailure(t *testing.T) {
	for _, status := range []int{200, 400, 403, 422, 429, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			upstream := &codexModelsHTTPUpstreamStub{do: func(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
				calls++
				require.Equal(t, "bps.openai.com", req.URL.Host)
				require.Equal(t, "/basispoints/api/images/generations", req.URL.Path)
				require.True(t, HTTPUpstreamSingleSendRequired(req.Context()))
				require.Equal(t, "Excel", req.Header.Get("X-Openai-Internal-Basispoints-Office-Host"))
				require.Empty(t, req.Header.Get("Cookie"))
				body := `{"data":[{"b64_json":"aW1hZ2U=","output_format":"png"}],"usage":{"input_tokens":10,"output_tokens":20,"cache_creation_input_tokens":4}}`
				if status != 200 {
					body = `{"error":{"code":"rejected","message":"rejected"}}`
				}
				return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
			}}
			svc := newOpenAIImagesTestService(upstream)
			body := []byte(`{"model":"gpt-image-2","prompt":"draw","size":"1536x1024","response_format":"url"}`)
			c, rec := newOpenAIImagesTestContext(t, body)
			p, err := svc.ParseOpenAIImagesRequest(c, body)
			require.NoError(t, err)
			result, err := svc.ForwardImages(context.Background(), c, excelBPSAccount(t), body, p, "")
			require.Equal(t, 1, calls)
			if status == 200 {
				require.NoError(t, err)
				require.Equal(t, 1, result.ImageCount)
				require.Equal(t, 10, result.Usage.InputTokens)
				require.Equal(t, 20, result.Usage.OutputTokens)
				require.Zero(t, result.Usage.CacheCreationInputTokens)
				require.Equal(t, 4, result.UpstreamUsage.CacheCreationInputTokens)
				require.EqualValues(t, 0, gjson.GetBytes(rec.Body.Bytes(), "usage.cache_creation_input_tokens").Int())
				require.EqualValues(t, 4, gjson.GetBytes(rec.Body.Bytes(), "usage.upstream_usage.cache_creation_input_tokens").Int())
				require.Contains(t, rec.Body.String(), "data:image/png;base64,")
			} else {
				require.Nil(t, result)
				var typed *OpenAIImagesUpstreamError
				require.ErrorAs(t, err, &typed)
				var replay *UpstreamFailoverError
				require.NotErrorAs(t, err, &replay)
				require.Equal(t, status, rec.Code)
			}
		})
	}
}
