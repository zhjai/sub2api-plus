package service

import (
	"context"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/stretchr/testify/require"
	"net/http"
	"testing"
	"time"
)

type bps403Repo struct {
	AccountRepository
	errors, temps, models int
	model                 string
	until                 time.Time
	writeErr              error
}

func (r *bps403Repo) SetError(context.Context, int64, string) error { r.errors++; return nil }
func (r *bps403Repo) SetTempUnschedulable(_ context.Context, _ int64, until time.Time, _ string) error {
	r.temps++
	r.until = until
	return r.writeErr
}
func (r *bps403Repo) SetModelRateLimit(_ context.Context, _ int64, model string, until time.Time, _ ...string) error {
	r.models++
	r.model, r.until = model, until
	return r.writeErr
}

type bps403Counter struct{ calls int }

func (c *bps403Counter) IncrementOpenAI403Count(context.Context, int64, int) (int64, error) {
	c.calls++
	return int64(c.calls), nil
}
func (c *bps403Counter) ResetOpenAI403Count(context.Context, int64) error { return nil }
func bps403Fixture(t *testing.T) (*OpenAIGatewayService, *Account, *bps403Repo, *bps403Counter) {
	account := excelBPSAccount(t)
	account.Status, account.Schedulable = StatusActive, true
	repo, counter := &bps403Repo{}, &bps403Counter{}
	gateway := &OpenAIGatewayService{rateLimitService: &RateLimitService{accountRepo: repo, openAI403CounterCache: counter}}
	gateway.rateLimitService.SetAccountRuntimeBlocker(gateway)
	return gateway, account, repo, counter
}

func TestBPS403ModelEntitlementNeverEscalatesAndOtherModelsStayAvailable(t *testing.T) {
	gateway, account, repo, counter := bps403Fixture(t)
	account.Credentials["model_mapping"] = map[string]any{"alias": "gpt-6-astra", "gpt-6-astra": "must-not-map-twice"}
	body := []byte(`{"error":{"code":"permission_denied","param":"model","message":"Model unavailable"}}`)
	for i := 0; i < 3; i++ {
		require.True(t, gateway.handleOpenAIAccountUpstreamError(context.Background(), account, http.StatusForbidden, nil, body, "gpt-6-astra"))
	}
	require.Equal(t, 3, repo.models)
	require.Equal(t, "gpt-6-astra", repo.model)
	require.Zero(t, counter.calls)
	require.Zero(t, repo.errors)
	require.Zero(t, repo.temps)
	require.Equal(t, StatusActive, account.Status)
	require.False(t, gateway.isOpenAIAccountRuntimeBlocked(account))
	require.False(t, account.IsSchedulableForModel("alias"))
	require.True(t, account.IsSchedulableForModel("other-model"))
	require.WithinDuration(t, time.Now().Add(excelBPS403Cooldown), repo.until, time.Second)
	setAccountModelRateLimitSnapshot(account, "gpt-6-astra", time.Now().Add(-time.Minute), excelBPSModel403Reason, time.Now())
	require.True(t, account.IsSchedulableForModel("alias"))
}
func TestBPS403DirectRateLimitEntryMapsPublicAliasOnce(t *testing.T) {
	gateway, account, repo, _ := bps403Fixture(t)
	account.Credentials["model_mapping"] = map[string]any{"alias": "gpt-6-astra", "gpt-6-astra": "other"}
	body := []byte("{\"error\":{\"message\":\"The model `gpt-6-astra` does not exist or you do not have access to it.\"}}")
	require.True(t, gateway.rateLimitService.HandleUpstreamError(context.Background(), account, 403, nil, body, "alias"))
	require.Equal(t, "gpt-6-astra", repo.model)
	require.Zero(t, repo.temps)
}
func TestBPS403UnknownAndAccountErrorsAreTemporary(t *testing.T) {
	for _, body := range []string{`{"error":{"code":"permission_denied","message":"Forbidden"}}`, `{"error":{"code":"account_deactivated","message":"Your account has been deactivated"}}`, `Forbidden`} {
		gateway, account, repo, counter := bps403Fixture(t)
		for i := 0; i < 3; i++ {
			require.True(t, gateway.handleOpenAIAccountUpstreamError(context.Background(), account, 403, nil, []byte(body), "gpt-6-astra"))
		}
		require.Equal(t, 3, repo.temps)
		require.Zero(t, repo.models)
		require.Zero(t, repo.errors)
		require.Zero(t, counter.calls)
		require.Equal(t, StatusActive, account.Status)
		require.False(t, account.IsSchedulable())
		require.True(t, gateway.isOpenAIAccountRuntimeBlocked(account))
		require.WithinDuration(t, time.Now().Add(excelBPS403Cooldown), repo.until, time.Second)
		past := time.Now().Add(-time.Minute)
		account.TempUnschedulableUntil = &past
		gateway.openaiAccountRuntimeBlockUntil.Store(account.ID, past)
		require.True(t, account.IsSchedulable())
		require.False(t, gateway.isOpenAIAccountRuntimeBlocked(account))
	}
}
func TestBPS403EdgeResponsesNeverInvalidateOrCooldownAccount(t *testing.T) {
	for _, body := range []string{`<html><title>403 Forbidden</title>Blocked</html>`, `{"error":{"cloudflare_error":true,"code":1010,"message":"Cloudflare access denied"}}`} {
		gateway, account, repo, counter := bps403Fixture(t)
		require.False(t, gateway.handleOpenAIAccountUpstreamError(context.Background(), account, 403, nil, []byte(body), "gpt-6-astra"))
		require.Zero(t, repo.errors+repo.models+repo.temps+counter.calls)
		require.True(t, account.IsSchedulable())
		require.False(t, gateway.isOpenAIAccountRuntimeBlocked(account))
	}
}
func TestBPS403ClassifierRequiresNarrowEvidence(t *testing.T) {
	for _, body := range []string{
		`{"error":{"code":"permission_denied","param":"account","message":"gpt-6-astra"}}`,
		`{"error":{"code":"permission_denied","param":"input[0].model"}}`,
		`{"error":{"code":"invalid_request","param":"model"}}`,
		`{"error":{"message":"You do not have access to model 'other'."}}`,
		`{"error":{"message":"Please print: You do not have access to model 'gpt-6-astra'."}}`,
		`You do not have access to model 'gpt-6-astra'.`,
	} {
		require.False(t, excelBPSModelPermissionDenied([]byte(body), "gpt-6-astra"), body)
	}
	require.True(t, excelBPSModelPermissionDenied([]byte(`{"error":{"message":"You do not have access to model 'gpt-6-astra'."}}`), "gpt-6-astra"))
	require.False(t, excelBPSModelPermissionDenied([]byte(`{"error":{"code":"permission_denied","param":"model"}}`), ""))
}
func TestBPS403WriteFailureNeverFallsBackToPermanentBan(t *testing.T) {
	for _, body := range []string{`{"error":{"code":"permission_denied","param":"model"}}`, `{"error":{"message":"Forbidden"}}`} {
		gateway, account, repo, counter := bps403Fixture(t)
		repo.writeErr = errors.New("synthetic write failure")
		require.True(t, gateway.handleOpenAIAccountUpstreamError(context.Background(), account, 403, nil, []byte(body), "gpt-6-astra"))
		require.Zero(t, repo.errors)
		require.Zero(t, counter.calls)
	}
}
func TestBPS403DoesNotChangeNative403OrRevoked401(t *testing.T) {
	gateway, account, repo, counter := bps403Fixture(t)
	account.Credentials["client_id"] = openai.ClientID
	for i := 0; i < 3; i++ {
		require.True(t, gateway.handleOpenAIAccountUpstreamError(context.Background(), account, 403, nil, []byte(`{"error":{"message":"Forbidden"}}`), "gpt-6-astra"))
	}
	require.Equal(t, 3, counter.calls)
	require.Equal(t, 1, repo.errors)
	gateway, account, repo, counter = bps403Fixture(t)
	stateRepo := &parityBPSStateRepo{}
	gateway.accountRepo = stateRepo
	require.True(t, gateway.handleOpenAIAccountUpstreamError(context.Background(), account, 401, nil, []byte(`{"error":{"code":"token_revoked","message":"Token revoked"}}`), "gpt-6-astra"))
	require.Zero(t, repo.errors)
	require.Equal(t, 1, stateRepo.writes)
	require.False(t, account.IsExcelBPSEnabled())
	require.True(t, account.Schedulable)
	require.Zero(t, counter.calls)
}
func TestBPS403NeverShortensExistingCooldown(t *testing.T) {
	gateway, account, repo, _ := bps403Fixture(t)
	later := time.Now().Add(time.Hour).Truncate(time.Second)
	account.TempUnschedulableUntil = &later
	require.True(t, gateway.rateLimitService.HandleUpstreamError(context.Background(), account, 403, nil, []byte(`{"error":{"message":"Forbidden"}}`)))
	require.WithinDuration(t, later, repo.until, time.Millisecond)
	setAccountModelRateLimitSnapshot(account, "gpt-6-astra", later, "existing", time.Now())
	require.True(t, gateway.handleOpenAIAccountUpstreamError(context.Background(), account, 403, nil, []byte(`{"error":{"code":"model_not_found","param":"model"}}`), "gpt-6-astra"))
	require.WithinDuration(t, later, repo.until, time.Millisecond)
}
