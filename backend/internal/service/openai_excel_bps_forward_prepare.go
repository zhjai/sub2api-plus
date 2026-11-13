package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service/basispoints"
)

var excelBPSAttachments basispoints.AttachmentCache

type excelBPSAttachmentRequestKey struct{}

// Prepare against placeholder attachment IDs first: every message, tool and
// image must pass local validation before any upload can spend account quota.
// Request-scoped persistent wrappers never retain a completed request context.
func (s *OpenAIGatewayService) prepareExcelBPSForwardRequest(ctx context.Context, raw []byte, token string, account *Account, scope string, cached bool) (*http.Request, *basispoints.Bridge, error) {
	if err := validateExcelOAuthAccount(account, token); err != nil {
		return nil, nil, err
	}
	raw, err := applyExcelBPSOptions(raw, account)
	if err != nil {
		return nil, nil, err
	}
	raw, err = ApplyUpstreamClientTimezone(ctx, account, raw, upstreamTimezoneAccountProxy(account))
	if err != nil {
		return nil, nil, err
	}
	plan, err := basispoints.PrepareNativeImages(raw)
	if err != nil {
		return nil, nil, err
	}
	var replay *basispoints.ReplayCache
	var catalog *basispoints.CatalogCache
	if cached {
		replay, catalog = s.newExcelBPSPersistentCaches(ctx)
	}
	prepared, bridge, err := plan.PrepareWithCatalog(scope, replay, catalog)
	if err != nil {
		return nil, nil, excelBPSPrepareStateError(err)
	}
	if plan.HasImages() {
		// Cache IDs only within the same credential generation, account, key and
		// conversation. Anonymous requests deliberately do not reuse attachment IDs.
		attachmentScope := ""
		if cached {
			sum := sha256.Sum256([]byte(scope + "\x00" + token))
			attachmentScope = hex.EncodeToString(sum[:])
		}
		_, err = plan.Upload(ctx, &excelBPSAttachments, attachmentScope, func(ctx context.Context, image basispoints.InlineAttachment) (string, error) {
			return s.uploadExcelBPSAttachment(ctx, token, account, image)
		})
		if err != nil {
			return nil, nil, err
		}
		uploaded, bodyErr := plan.Body()
		if bodyErr != nil {
			return nil, nil, bodyErr
		}
		prepared, bridge, err = bridge.Reprepare(uploaded)
		if err != nil {
			return nil, nil, excelBPSPrepareStateError(err)
		}
	}
	req, err := newExcelBPSRequest(ctx, prepared, token, excelOAuthAccountID(token))
	return req, bridge, err
}

func excelBPSPrepareStateError(err error) error {
	if errors.Is(err, basispoints.ErrStateUnavailable) {
		return infraerrors.New(http.StatusServiceUnavailable, "basispoints_state_unavailable", "BPS session state is unavailable. Retry after the state service recovers.").WithCause(err)
	}
	if errors.Is(err, basispoints.ErrStateCapacity) {
		return infraerrors.New(http.StatusRequestEntityTooLarge, "basispoints_state_capacity", "BPS session state exceeds the supported capacity.").WithCause(err)
	}
	return err
}

func (s *OpenAIGatewayService) uploadExcelBPSAttachment(ctx context.Context, token string, account *Account, image basispoints.InlineAttachment) (string, error) {
	body, contentType, length, err := image.Multipart()
	if err != nil {
		return "", err
	}
	req, err := newExcelBPSRequest(ctx, nil, token, excelOAuthAccountID(token))
	if err != nil {
		return "", err
	}
	req.URL.Path = "/basispoints/api/attachments"
	req = req.WithContext(context.WithValue(req.Context(), excelBPSAttachmentRequestKey{}, true))
	req.Body = io.NopCloser(body)
	req.GetBody = nil // A multipart upload is never silently replayed.
	req.ContentLength = length
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")
	resp, err := s.doAccountRPMUpstream(req, upstreamTimezoneAccountProxy(account), account)
	if err != nil {
		if IsAccountRPMError(err) || ctx.Err() != nil {
			return "", err
		}
		return "", infraerrors.New(http.StatusBadGateway, "basispoints_attachment_failed", "BPS attachment upload failed.")
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return "", infraerrors.New(http.StatusBadGateway, "basispoints_attachment_failed", "BPS returned an invalid attachment response.")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// This is an attachment failure, not an inference replay instruction. Do
		// not forward arbitrary upstream bodies or account credentials to clients.
		return "", infraerrors.New(http.StatusBadGateway, "basispoints_attachment_failed", fmt.Sprintf("BPS attachment upload returned HTTP %d.", resp.StatusCode))
	}
	var result struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(raw, &result) != nil || !basispoints.ValidAttachmentID(result.ID) {
		return "", infraerrors.New(http.StatusBadGateway, "basispoints_attachment_failed", "BPS returned an invalid attachment ID.")
	}
	return result.ID, nil
}
