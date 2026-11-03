package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSettingsOpenAIWSModeRouterV2Contract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const key = service.SettingKeyOpenAIWSModeRouterV2Enabled
	for _, initial := range []bool{false, true} {
		t.Run(strconv.FormatBool(initial), func(t *testing.T) {
			repo := &settingHandlerRepoStub{values: map[string]string{}}
			cfg := &config.Config{Default: config.DefaultConfig{UserConcurrency: 5}}
			cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = initial
			svc := service.NewSettingService(repo, cfg)
			h := NewSettingHandler(svc, nil, nil, nil, nil, nil, nil)
			checkResponse := func(rec *httptest.ResponseRecorder, want bool) {
				t.Helper()
				require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
				var body struct {
					Data map[string]json.RawMessage `json:"data"`
				}
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
				require.Equal(t, strconv.FormatBool(want), string(body.Data[key]))
			}
			get := func(want bool) {
				t.Helper()
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings", nil)
				h.GetSettings(c)
				checkResponse(rec, want)
			}
			get(initial)
			checkResponse(doUpdateSettings(t, h, map[string]any{"site_name": "legacy client"}, nil), initial)
			require.NotContains(t, repo.values, key, "omission must keep inheriting config")
			for _, enabled := range []bool{true, false} {
				checkResponse(doUpdateSettings(t, h, map[string]any{key: enabled}, nil), enabled)
				require.Equal(t, strconv.FormatBool(enabled), repo.values[key])
				require.Equal(t, enabled, cfg.OpenAIWSModeRouterV2Enabled())
				get(enabled)
				for _, payload := range []map[string]any{{"site_name": "old client"}, {key: nil}} {
					checkResponse(doUpdateSettings(t, h, payload, nil), enabled)
					require.Equal(t, strconv.FormatBool(enabled), repo.values[key])
					require.Equal(t, enabled, cfg.OpenAIWSModeRouterV2Enabled())
				}
			}
			rec := doUpdateSettings(t, h, map[string]any{key: "true"}, nil)
			require.Equal(t, http.StatusBadRequest, rec.Code)
			require.Equal(t, "false", repo.values[key])
		})
	}
}

func TestSettingsOpenAIWSModeRouterV2Audit(t *testing.T) {
	before := &service.SystemSettings{}
	after := &service.SystemSettings{OpenAIWSModeRouterV2Enabled: true}
	changed := diffSettings(before, after, nil, nil, UpdateSettingsRequest{})
	require.Contains(t, changed, service.SettingKeyOpenAIWSModeRouterV2Enabled)
	changed = diffSettings(after, before, nil, nil, UpdateSettingsRequest{})
	require.Contains(t, changed, service.SettingKeyOpenAIWSModeRouterV2Enabled)
	changed = diffSettings(after, after, nil, nil, UpdateSettingsRequest{})
	require.NotContains(t, changed, service.SettingKeyOpenAIWSModeRouterV2Enabled)
	// Explicit false is present in the DTO, not omitted by JSON encoding.
	raw, err := json.Marshal(dto.SystemSettings{})
	require.NoError(t, err)
	require.Contains(t, string(raw), `"openai_ws_mode_router_v2_enabled":false`)
}
