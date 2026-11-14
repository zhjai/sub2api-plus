package service

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"io"
	"net/http"
	"strings"
	"time"
)

type ExcelBPSStateRepository interface {
	CompareAndSwapExcelBPSExtra(context.Context, *Account, map[string]any) (bool, error)
}

func (a *Account) ExcelBPS403RecoveryPending() bool {
	if a == nil || !a.IsActive() || !a.Schedulable || !a.excelBPSRouteRequested() || a.ExcelBPSCredentialAccount() == nil || !bpsBool(a.Extra, ExcelBPSAutoDisableOn403Key, true) || !bpsBool(a.Extra, ExcelBPSAutoRecoverOn403Key, true) || a.Extra[ExcelBPS403DisabledReasonKey] != "upstream_403" {
		return false
	}
	if a.AutoPauseOnExpired && a.ExpiresAt != nil && !time.Now().Before(*a.ExpiresAt) {
		return false
	}
	_, err := time.Parse(time.RFC3339Nano, mapString(a.Extra, ExcelBPS403DisabledAtKey))
	return err == nil
}
func (a *Account) ExcelBPS403RecoveryDue(now time.Time) bool {
	if !a.ExcelBPS403RecoveryPending() {
		return false
	}
	if a.AutoPauseOnExpired && a.ExpiresAt != nil && !now.Before(*a.ExpiresAt) {
		return false
	}
	at, err := time.Parse(time.RFC3339Nano, mapString(a.Extra, ExcelBPS403DisabledAtKey))
	if err != nil {
		return false
	}
	if raw, ok := a.Extra[ExcelBPS403LastProbeAtKey]; ok && raw != nil {
		last, err := time.Parse(time.RFC3339Nano, mapString(a.Extra, ExcelBPS403LastProbeAtKey))
		if err != nil {
			return false
		}
		if last.After(at) {
			at = last
		}
	}
	return !now.Before(at.Add(a.ExcelBPS403RecoveryInterval()))
}
func (s *OpenAIGatewayService) RunExcelBPS403Recovery(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.SyncExcelBPS403Recovery(ctx)
		}
	}
}
func (s *OpenAIGatewayService) SyncExcelBPS403Recovery(ctx context.Context) {
	if s == nil || s.accountRepo == nil || s.concurrencyService == nil {
		return
	}
	repo, ok := s.accountRepo.(ExcelBPSStateRepository)
	if !ok {
		return
	}
	queryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	accounts, err := s.accountRepo.ListByPlatform(queryCtx, PlatformOpenAI)
	cancel()
	if err != nil {
		return
	}
	for i := range accounts {
		if ctx.Err() != nil {
			return
		}
		a := &accounts[i]
		now := time.Now()
		if !a.ExcelBPS403RecoveryDue(now) {
			continue
		}
		probeCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		slot, err := s.concurrencyService.AcquireAccountSlot(probeCtx, a.ID, a.Concurrency)
		if err != nil || slot == nil || !slot.Acquired {
			cancel()
			continue
		}
		patch := map[string]any{ExcelBPS403LastProbeAtKey: now.UTC().Format(time.RFC3339Nano)}
		claimed, err := repo.CompareAndSwapExcelBPSExtra(probeCtx, a, patch)
		if claimed && err == nil {
			a.Extra = shallowCopyMap(a.Extra)
			a.Extra[ExcelBPS403LastProbeAtKey] = patch[ExcelBPS403LastProbeAtKey]
			if s.probeExcelBPS403Recovery(context.WithValue(probeCtx, excelBPSRecoveryDispatchKey{}, patch[ExcelBPS403LastProbeAtKey]), a) == nil {
				// A grant refresh by this probe may have rotated its own credentials.
				// Re-read only if the same claim/generation and manual eligibility remain.
				latest, readErr := s.accountRepo.GetByID(probeCtx, a.ID)
				if readErr == nil && latest != nil && latest.excelBPSRouteRequested() && excelBPSGrantFingerprint(latest.ExcelBPSCredentialAccount()) == excelBPSGrantFingerprint(a.ExcelBPSCredentialAccount()) && bpsBool(latest.Extra, ExcelBPSAutoRecoverOn403Key, true) && mapString(latest.Extra, ExcelBPS403LastProbeAtKey) == mapString(a.Extra, ExcelBPS403LastProbeAtKey) && mapString(latest.Extra, ExcelBPS403DisabledAtKey) == mapString(a.Extra, ExcelBPS403DisabledAtKey) {
					_, _ = repo.CompareAndSwapExcelBPSExtra(probeCtx, latest, map[string]any{ExcelBPS403DisabledAtKey: nil, ExcelBPS403LastProbeAtKey: nil, ExcelBPS403DisabledReasonKey: nil, ExcelBPS403ProbeModelKey: nil})
				}
			}
		}
		slot.ReleaseFunc()
		cancel()
	}
}
func (s *OpenAIGatewayService) probeExcelBPS403Recovery(ctx context.Context, account *Account) (probeErr error) {
	a := account.ExcelBPSCredentialAccount()
	if a == nil {
		return errors.New("BPS grant missing")
	}
	model := mapString(account.Extra, ExcelBPS403ProbeModelKey)
	if model == "" {
		return errors.New("BPS recovery model missing")
	}
	// Recovery uses the same configured session-proxy pool as real traffic.
	selected, lease, selectErr := s.acquireExcelBPSSessionProxy(ctx, nil, a, []byte(`{"input":"BPS recovery"}`))
	if selectErr != nil {
		return selectErr
	}
	a = selected
	defer func() { lease.release(probeErr) }()
	token, _, err := s.GetAccessToken(ctx, a)
	if err != nil {
		return err
	}
	nonce := uuid.NewString()
	raw, _ := json.Marshal(map[string]any{"model": model, "stream": true, "input": "Reply with exactly this text: " + nonce})
	req, bridge, err := prepareExcelBPSRequest(ctx, raw, token, a, "", false)
	if err != nil {
		return err
	}
	proxy := ""
	if a.Proxy != nil {
		proxy = a.Proxy.URL()
	}
	resp, err := s.doAccountRPMUpstream(req, proxy, a)
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		return err
	}
	if resp == nil || resp.Body == nil || resp.StatusCode != http.StatusOK {
		return errors.New("BPS recovery HTTP failure")
	}
	converted := bridge.Stream(resp.Body)
	defer converted.Close()
	wire, err := io.ReadAll(io.LimitReader(converted, (2<<20)+1))
	if err != nil || len(wire) > 2<<20 {
		return errors.New("BPS recovery incomplete response")
	}
	if err := validateExcelBPSRecoveryResponse(wire, nonce); err != nil {
		return err
	}
	current, err := s.accountRepo.GetByID(ctx, account.ID)
	if err != nil || current == nil {
		return errors.New("BPS recovery credential verification unavailable")
	}
	currentGrant := current.ExcelBPSCredentialAccount()
	if currentGrant == nil || currentGrant.GetOpenAIAccessToken() != token {
		return errors.New("BPS recovery grant changed during probe")
	}
	account.Credentials = current.Credentials
	return nil
}
func validateExcelBPSRecoveryResponse(wire []byte, nonce string) error {
	completed := false
	for _, line := range strings.Split(string(wire), "\n") {
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if !gjson.Valid(data) {
			continue
		}
		event := gjson.Parse(data)
		switch event.Get("type").String() {
		case "response.failed", "response.incomplete", "error":
			return errors.New("BPS recovery terminal failure")
		case "response.completed":
			response := event.Get("response")
			if response.Get("status").String() != "completed" {
				return errors.New("BPS recovery not completed")
			}
			var text strings.Builder
			for _, item := range response.Get("output").Array() {
				if item.Get("type").String() != "message" {
					continue
				}
				for _, part := range item.Get("content").Array() {
					if part.Get("type").String() == "output_text" {
						text.WriteString(part.Get("text").String())
					}
				}
			}
			if strings.TrimSpace(text.String()) != nonce {
				return errors.New("BPS recovery response mismatch")
			}
			completed = true
		}
	}
	if !completed {
		return errors.New("BPS recovery missing completion")
	}
	return nil
}

type excelBPSRecoveryDispatchKey struct{}

// Re-check the persisted claim immediately before each actual transport send.
// A manual disable after the scan/claim therefore prevents the probe as well.
func validateExcelBPSRecoveryDispatch(ctx context.Context, repo accountRPMAccountReader, a *Account) error {
	claim, ok := ctx.Value(excelBPSRecoveryDispatchKey{}).(string)
	if !ok {
		return nil
	}
	if repo == nil || a == nil {
		return errors.New("BPS recovery account check unavailable")
	}
	latest, err := repo.GetByID(ctx, a.ID)
	if err != nil || latest == nil {
		return errors.New("BPS recovery account check failed")
	}
	if !latest.IsActive() || !latest.Schedulable || !latest.excelBPSRouteRequested() || !bpsBool(latest.Extra, ExcelBPSAutoRecoverOn403Key, true) || !bpsBool(latest.Extra, ExcelBPSAutoDisableOn403Key, true) || latest.Extra[ExcelBPS403DisabledReasonKey] != "upstream_403" || mapString(latest.Extra, ExcelBPS403LastProbeAtKey) != claim {
		return errors.New("BPS recovery claim no longer eligible")
	}
	return nil
}
