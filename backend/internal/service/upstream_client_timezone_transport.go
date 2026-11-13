package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strings"

	openaiwsv2 "github.com/Wei-Shaw/sub2api/internal/service/openai_ws_v2"
	coderws "github.com/coder/websocket"
)

func upstreamTimezoneAccountProxy(a *Account) string {
	if a != nil && a.ProxyID != nil && a.Proxy != nil {
		return a.Proxy.URL()
	}
	return ""
}

func applyUpstreamClientTimezoneHTTP(req *http.Request, a *Account, proxy string) (*http.Request, error) {
	mode, _ := upstreamTimezoneConfig(a)
	if mode == "off" || req.Body == nil {
		return req, nil
	}
	if contentType := req.Header.Get("Content-Type"); contentType != "" {
		mediaType, _, parseErr := mime.ParseMediaType(contentType)
		if parseErr == nil && mediaType != "application/json" && !strings.HasSuffix(mediaType, "+json") {
			// Multipart/media uploads cannot carry the Responses JSON metadata.
			return req, nil
		}
	}
	var reader io.ReadCloser
	var err error
	if req.GetBody != nil {
		reader, err = req.GetBody()
	} else {
		// Maintenance/evaluation callers can provide a streaming body without
		// GetBody. Read once, restore it immediately, and let the protocol-carrier
		// check decide whether any transformation is needed. This keeps an enabled
		// account from failing ordinary non-JSON or unmarked requests.
		reader = req.Body
	}
	if err != nil {
		return nil, err
	}
	const maxTimezoneHTTPBody = 64 << 20
	raw, err := io.ReadAll(io.LimitReader(reader, maxTimezoneHTTPBody+1))
	if req.GetBody != nil {
		_ = reader.Close()
	} else {
		_ = req.Body.Close()
	}
	if err != nil {
		return nil, err
	}
	if len(raw) > maxTimezoneHTTPBody {
		return nil, &UpstreamClientTimezoneError{Code: "protocol_body_too_large"}
	}
	restore := func(payload []byte) {
		req.Body = io.NopCloser(bytes.NewReader(payload))
		req.ContentLength = int64(len(payload))
	}
	clean, err := ApplyUpstreamClientTimezone(req.Context(), a, raw, proxy)
	if err != nil || bytes.Equal(raw, clean) {
		// Preserve the original stream when this helper was handed a one-shot
		// body. GetBody callers still retain their original request body.
		if req.GetBody == nil {
			restore(raw)
		}
		return req, err
	}
	next := req.Clone(req.Context())
	_ = req.Body.Close()
	next.Body = io.NopCloser(bytes.NewReader(clean))
	next.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(clean)), nil }
	next.ContentLength = int64(len(clean))
	next.Header.Del("Content-Length")
	return next, nil
}

type upstreamClientTimezoneWSKey struct{}

func withUpstreamClientTimezoneWS(ctx context.Context, a *Account) context.Context {
	return context.WithValue(ctx, upstreamClientTimezoneWSKey{}, a)
}

func upstreamClientTimezoneWSValue(ctx context.Context, value any) (any, error) {
	a, _ := ctx.Value(upstreamClientTimezoneWSKey{}).(*Account)
	mode, _ := upstreamTimezoneConfig(a)
	if mode == "off" {
		return value, nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	clean, err := ApplyUpstreamClientTimezone(ctx, a, raw, upstreamTimezoneAccountProxy(a))
	return json.RawMessage(clean), err
}

type upstreamClientTimezoneFrameConn struct {
	openaiwsv2.FrameConn
	account *Account
	proxy   string
}

func (c *upstreamClientTimezoneFrameConn) WriteFrame(ctx context.Context, kind coderws.MessageType, raw []byte) error {
	if (kind != coderws.MessageText && kind != coderws.MessageBinary) || !json.Valid(raw) {
		return c.FrameConn.WriteFrame(ctx, kind, raw)
	}
	clean, err := ApplyUpstreamClientTimezone(ctx, c.account, raw, c.proxy)
	if err != nil {
		return err
	}
	return c.FrameConn.WriteFrame(ctx, kind, clean)
}
