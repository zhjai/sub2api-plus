package routes

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type bpsCatalogRouteAccounts struct {
	service.AccountRepository
	accounts []service.Account
	err      error
}

func (r bpsCatalogRouteAccounts) ListSchedulableByGroupID(context.Context, int64) ([]service.Account, error) {
	return r.accounts, r.err
}
func (r bpsCatalogRouteAccounts) ListByGroup(context.Context, int64) ([]service.Account, error) {
	return r.accounts, r.err
}
func (r bpsCatalogRouteAccounts) ListModelAvailabilityCandidates(context.Context, *int64, []string, bool) ([]service.Account, error) {
	return r.accounts, r.err
}

type bpsCatalogRoutes struct {
	service.CompositeModelRouteRepository
	routes []service.CompositeModelRoute
	err    error
}

func (r bpsCatalogRoutes) ListByGroup(context.Context, int64, bool) ([]service.CompositeModelRoute, error) {
	return r.routes, r.err
}

func TestCompositeBPSCatalogThroughRealGatewayDispatch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, scenario := range []string{"pure", "mixed", "other endpoint", "route failure", "missing resolver", "account failure"} {
		t.Run(scenario, func(t *testing.T) {
			bps := service.Account{ID: 1, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Status: service.StatusActive, Schedulable: true, Credentials: map[string]any{"client_id": openai.ExcelClientID, "model_mapping": map[string]any{"gpt-6-astra": "gpt-6-astra"}}}
			accounts := bpsCatalogRouteAccounts{accounts: []service.Account{bps}}
			if scenario == "mixed" {
				accounts.accounts = append(accounts.accounts, service.Account{ID: 2, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Status: service.StatusActive, Schedulable: true, Credentials: map[string]any{}})
			}
			routes := bpsCatalogRoutes{routes: []service.CompositeModelRoute{{ID: 1, GroupID: 77, PublicModel: "gpt-6-astra", TargetPlatform: service.PlatformOpenAI, Endpoint: service.CompositeRouteEndpointResponses, Enabled: true}}}
			if scenario == "other endpoint" {
				routes.routes[0].Endpoint = service.CompositeRouteEndpointChatCompletions
				routes.routes[0].TargetPlatform = service.PlatformAnthropic
			}
			if scenario == "route failure" {
				routes.err = errors.New("route store unavailable")
			}
			if scenario == "account failure" {
				accounts.err = errors.New("account store unavailable")
			}
			resolver := service.NewCompositeRouteResolver(routes)
			if scenario == "missing resolver" {
				resolver = nil
			}
			svc := service.NewGatewayService(accounts,
				nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
				nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, resolver, nil, nil,
			)
			h := handler.NewGatewayHandler(svc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
			group := &service.Group{ID: 77, Platform: service.PlatformComposite, ModelAllowlist: service.GroupModelAllowlist{Enabled: true, Models: []string{"gpt-6-astra"}}}
			router := gin.New()
			router.Use(func(c *gin.Context) {
				c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{Group: group})
				c.Next()
			})
			router.Use(compositeTargetPlatformMiddleware(resolver))
			for _, path := range []string{"/v1/models", "/models", "/backend-api/codex/models"} {
				router.GET(path, func(c *gin.Context) {
					dispatchCodexModelsGateway(c, func(c *gin.Context) { t.Error("Composite reached native OpenAI handler") }, h.CodexModels)
				})
			}
			for _, path := range []string{"/v1/models", "/models", "/backend-api/codex/models"} {
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path+"?client_version=0.167.1", nil))
				if scenario == "route failure" || scenario == "missing resolver" || scenario == "account failure" {
					require.Equal(t, http.StatusServiceUnavailable, rec.Code, rec.Body.String())
					require.NotContains(t, rec.Body.String(), `"multi_agent_version":"v2"`)
					continue
				}
				require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
				require.Equal(t, int64(1), gjson.GetBytes(rec.Body.Bytes(), "models.#").Int())
				require.Equal(t, "null", gjson.GetBytes(rec.Body.Bytes(), "models.0.multi_agent_reasoning_effort").Raw)
				if scenario == "mixed" {
					require.Equal(t, "null", gjson.GetBytes(rec.Body.Bytes(), "models.0.multi_agent_version").Raw)
				} else {
					require.Equal(t, "v2", gjson.GetBytes(rec.Body.Bytes(), "models.0.multi_agent_version").String())
				}
				second := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodGet, path+"?client_version=0.167.1", nil)
				req.Header.Set("If-None-Match", rec.Header().Get("ETag"))
				router.ServeHTTP(second, req)
				require.Equal(t, http.StatusNotModified, second.Code)
			}
		})
	}
}
