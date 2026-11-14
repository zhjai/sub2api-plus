package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
)

// ExcelBPSCredentialAccount is a request-local view. Primary token caches and
// primary refresh persistence must never see a projected independent grant.
func (a *Account) ExcelBPSCredentialAccount() *Account {
	if a == nil || !a.IsOpenAIOAuth() || a.IsShadow() {
		return nil
	}
	if a.IsExcelOAuth() {
		return a
	}
	nested, ok := a.Credentials[ExcelBPSCredentialsKey].(map[string]any)
	if !ok || strings.TrimSpace(mapString(nested, "client_id")) != openai.ExcelClientID {
		return nil
	}
	clone := *a
	clone.excelBPSNativeCredentials = a.Credentials
	clone.Credentials = shallowCopyMap(a.Credentials)
	for _, k := range []string{"access_token", "refresh_token", "id_token", "client_id", "expires_at", "chatgpt_account_id", "chatgpt_user_id", "organization_id", "plan_type"} {
		delete(clone.Credentials, k)
	}
	for k, v := range nested {
		clone.Credentials[k] = v
	}
	delete(clone.Credentials, ExcelBPSCredentialsKey)
	return &clone
}
func mapString(m map[string]any, key string) string { s, _ := m[key].(string); return s }
func (a *Account) BPSAccountForModel(model string) *Account {
	if !a.IsExcelBPSEnabledForModel(model) {
		return nil
	}
	return a.ExcelBPSCredentialAccount()
}
func ExcelBPSNativeFallbackAccount(a *Account) *Account {
	if a == nil {
		return nil
	}
	clone := *a
	if a.excelBPSNativeCredentials != nil {
		clone.Credentials = a.excelBPSNativeCredentials
		clone.excelBPSNativeCredentials = nil
	}
	if clone.IsExcelOAuth() || strings.TrimSpace(clone.GetOpenAIAccessToken()) == "" {
		return nil
	}
	clone.Extra = shallowCopyMap(a.Extra)
	if clone.Extra == nil {
		clone.Extra = map[string]any{}
	}
	clone.Extra[ExcelBPSEnabledKey] = false
	return &clone
}
func ValidateExcelBPSIndependentGrant(a *Account) error {
	if a == nil {
		return nil
	}
	raw, present := a.Credentials[ExcelBPSCredentialsKey]
	if !present {
		return nil
	}
	if raw == nil {
		return nil
	}
	grant := a.ExcelBPSCredentialAccount()
	if grant == nil || a.IsExcelOAuth() {
		return errors.New("independent BPS grant requires a native OpenAI OAuth account and an Excel client grant")
	}
	if grant.GetOpenAIRefreshToken() == "" {
		return errors.New("independent BPS grant requires a refresh token")
	}
	if err := validateExcelOAuthAccount(grant, grant.GetOpenAIAccessToken()); err != nil {
		return err
	}
	return ValidateExcelBPSIndependentWorkspace(a)
}

// Existing grants may be expired while waiting for refresh. Ordinary primary
// edits validate their durable binding without requiring a currently live BPS
// access token. Explicit attachment/replacement still uses the full validator.
func ValidateExcelBPSIndependentWorkspace(a *Account) error {
	if a == nil || a.Credentials[ExcelBPSCredentialsKey] == nil {
		return nil
	}
	grant := a.ExcelBPSCredentialAccount()
	if grant == nil || a.IsExcelOAuth() {
		return errors.New("independent BPS grant requires a native OpenAI OAuth account and an Excel client grant")
	}
	if nativeID := a.GetCredential("chatgpt_account_id"); nativeID != "" && nativeID != excelOAuthAccountID(grant.GetOpenAIAccessToken()) {
		return errors.New("independent BPS grant must belong to the same workspace")
	}
	return nil
}

// CAS changes only the nested grant, never primary tokens or route settings.
type ExcelBPSGrantRepository interface {
	ReplaceExcelBPSGrant(context.Context, int64, map[string]any, map[string]any) (bool, error)
}

func (p *OpenAITokenProvider) getIndependentExcelBPSToken(ctx context.Context, projected *Account) (string, error) {
	if p == nil || p.accountRepo == nil {
		return "", errors.New("BPS credential lookup unavailable")
	}
	info, err := refreshIndependentExcelBPSGrant(ctx, p.accountRepo, p.openAIOAuthService, projected.ID, false)
	if err != nil {
		return "", err
	}
	return info.AccessToken, nil
}
func refreshIndependentExcelBPSGrant(ctx context.Context, repo AccountRepository, oauth *OpenAIOAuthService, id int64, force bool) (*OpenAITokenInfo, error) {
	current, err := repo.GetByID(ctx, id)
	if err != nil || current == nil {
		return nil, errors.New("BPS credential lookup failed")
	}
	grant := current.ExcelBPSCredentialAccount()
	if grant == nil || current.IsExcelOAuth() {
		return nil, errors.New("independent BPS grant missing")
	}
	expiry := grant.GetCredentialAsTime("expires_at")
	if !force && expiry != nil && time.Until(*expiry) > openAITokenRefreshSkew {
		return &OpenAITokenInfo{AccessToken: grant.GetOpenAIAccessToken()}, nil
	}
	store, ok := repo.(ExcelBPSGrantRepository)
	state, hasState := repo.(ExcelBPSStateRepository)
	if !ok || !hasState || oauth == nil {
		return nil, errors.New("independent BPS refresh service unavailable")
	}
	if until, parseErr := time.Parse(time.RFC3339Nano, mapString(current.Extra, "openai_excel_bps_grant_refresh_until")); parseErr == nil && time.Now().Before(until) {
		return nil, errors.New("BPS grant refresh in progress; retry later")
	}
	claimed, err := state.CompareAndSwapExcelBPSExtra(ctx, current, map[string]any{"openai_excel_bps_grant_refresh_until": time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)})
	if err != nil || !claimed {
		return nil, errors.New("BPS grant refresh claim unavailable")
	}
	refreshCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	info, err := oauth.RefreshAccountToken(refreshCtx, grant)
	if err != nil {
		return nil, errors.New("independent BPS grant refresh failed; native grant remains unchanged")
	}
	previous, _ := current.Credentials[ExcelBPSCredentialsKey].(map[string]any)
	next := shallowCopyMap(previous)
	for k, v := range oauth.BuildAccountCredentials(info) {
		next[k] = v
	}
	changed, err := store.ReplaceExcelBPSGrant(refreshCtx, current.ID, previous, next)
	if err != nil || !changed {
		return nil, errors.New("BPS grant changed during refresh; retry")
	}
	return info, nil
}
func (s *adminServiceImpl) RefreshIndependentExcelBPSGrant(ctx context.Context, id int64, oauth *OpenAIOAuthService) (*Account, error) {
	if _, err := refreshIndependentExcelBPSGrant(ctx, s.accountRepo, oauth, id, true); err != nil {
		return nil, err
	}
	return s.accountRepo.GetByID(ctx, id)
}

// ExcelBPSPersistenceCredentials is for durable repository comparisons only.
func ExcelBPSPersistenceCredentials(a *Account) map[string]any {
	if a.excelBPSNativeCredentials != nil {
		return a.excelBPSNativeCredentials
	}
	return a.Credentials
}

// Ordinary native edits may have loaded an older snapshot while the independent
// grant rotated. Preserve the current nested grant under the repository row lock.
func PreserveExcelBPSIndependentGrant(a *Account, current map[string]any) {
	if a == nil || a.excelBPSGrantExplicit {
		return
	}
	a.Credentials = shallowCopyMap(a.Credentials)
	if v, ok := current[ExcelBPSCredentialsKey]; ok {
		if a.Credentials == nil {
			a.Credentials = map[string]any{}
		}
		a.Credentials[ExcelBPSCredentialsKey] = v
	} else {
		delete(a.Credentials, ExcelBPSCredentialsKey)
	}
}
