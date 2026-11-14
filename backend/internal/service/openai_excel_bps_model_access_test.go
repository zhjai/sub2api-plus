package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"testing"
	"time"
)

func TestExcelBPSAccessCatalogIsAuthoritative(t *testing.T) {
	cases := []struct {
		name, raw string
		count     int
		fail      bool
	}{
		{"denied", `{"allowed":false}`, 0, false},
		{"empty", `{"allowed":true,"model_catalog":{"models":[]}}`, 0, false},
		{"missing", `{"allowed":true}`, 0, true},
		{"unknown", `{"model_catalog":{"models":[]}}`, 0, true},
		{"filter", `{"allowed":true,"model_catalog":{"restricted_models":["gpt-6-luna"],"models":[{"id":"gpt-6-astra","label":"Astra","efforts":[{"value":"high"},{"value":"high"},{"value":"ultra"}]},{"id":"gpt-6-astra"},{"id":"gpt-6-luna"},{"id":"gpt-6-sol","available":false},{"id":"gpt-5.6-sol","policy":{"state":"disabled"}},{"id":"<script>"}]}}`, 1, false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := excelBPSAccessModelsBody([]byte(tt.raw))
			if tt.fail {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, int64(tt.count), gjson.GetBytes(raw, "data.#").Int())
			if tt.count > 0 {
				require.Equal(t, "gpt-6-astra", gjson.GetBytes(raw, "data.0.id").String())
				require.Equal(t, `["high"]`, gjson.GetBytes(raw, "data.0.reasoning_efforts").Raw)
			}
		})
	}
}

func TestExcelBPSAccessDiscoveryDoesNotReuseOldPermissions(t *testing.T) {
	a := excelBPSAccount(t)
	upstream := &httpUpstreamRecorder{resp: excelBPSMockResponse(200, `{"allowed":true,"model_catalog":{"models":[{"id":"gpt-6-astra"}]}}`)}
	svc := openAIClientToolsTestService(upstream)
	first, err := svc.FetchExcelBPSModelsList(context.Background(), a)
	require.NoError(t, err)
	first.Body[0] = 'x' // caller cannot corrupt the cached response
	second, err := svc.FetchExcelBPSModelsList(context.Background(), a)
	require.NoError(t, err)
	require.True(t, gjson.ValidBytes(second.Body))
	require.Len(t, upstream.requests, 1)
	// Rotating the bearer changes the discovery cache identity even for the same account.
	a.Credentials["access_token"] = a.GetOpenAIAccessToken() + "-rotated"
	upstream.resp = excelBPSMockResponse(200, `{"allowed":false}`)
	denied, err := svc.FetchExcelBPSModelsList(context.Background(), a)
	require.NoError(t, err)
	require.Equal(t, int64(0), gjson.GetBytes(denied.Body, "data.#").Int())
	require.Len(t, upstream.requests, 2)
	// An expired cache must not conceal a current upstream error.
	svc.excelBPSModelsCache.mu.Lock()
	for k, v := range svc.excelBPSModelsCache.entries {
		v.expiresAt = time.Now().Add(-time.Minute)
		svc.excelBPSModelsCache.entries[k] = v
	}
	svc.excelBPSModelsCache.mu.Unlock()
	upstream.resp = excelBPSMockResponse(403, `{"error":"forbidden"}`)
	_, err = svc.FetchExcelBPSModelsList(context.Background(), a)
	require.Error(t, err)
	require.Len(t, upstream.requests, 3)
}
