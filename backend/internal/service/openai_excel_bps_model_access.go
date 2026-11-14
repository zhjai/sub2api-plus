package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var excelBPSModelID = regexp.MustCompile(`^gpt-[a-z0-9]+(?:[._-][a-z0-9]+)*$`)

// Convert only the official access response. Empty/denied access is authoritative;
// malformed envelopes and HTTP failures cannot manufacture a local allowlist.
func excelBPSAccessModelsBody(raw []byte) ([]byte, error) {
	var envelope struct {
		Allowed *bool `json:"allowed"`
		Catalog *struct {
			Models     *[]json.RawMessage `json:"models"`
			Restricted []string           `json:"restricted_models"`
		} `json:"model_catalog"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil || envelope.Allowed == nil {
		return nil, fmt.Errorf("invalid Excel BPS access response")
	}
	models := make([]map[string]any, 0)
	if *envelope.Allowed {
		if envelope.Catalog == nil || envelope.Catalog.Models == nil {
			return nil, fmt.Errorf("missing Excel BPS model catalog")
		}
		restricted := map[string]bool{}
		for _, id := range envelope.Catalog.Restricted {
			restricted[id] = true
		}
		seen := map[string]bool{}
		for _, raw := range *envelope.Catalog.Models {
			var entry struct {
				ID        string `json:"id"`
				Label     string `json:"label"`
				Available *bool  `json:"available"`
				Enabled   *bool  `json:"enabled"`
				Picker    *bool  `json:"model_picker_enabled"`
				Policy    *struct {
					State string `json:"state"`
				} `json:"policy"`
				Efforts []struct {
					Value string `json:"value"`
				} `json:"efforts"`
			}
			if json.Unmarshal(raw, &entry) != nil || !excelBPSModelID.MatchString(entry.ID) || restricted[entry.ID] || seen[entry.ID] {
				continue
			}
			if entry.Available != nil && !*entry.Available || entry.Enabled != nil && !*entry.Enabled || entry.Picker != nil && !*entry.Picker || entry.Policy != nil && (entry.Policy.State == "disabled" || entry.Policy.State == "unconfigured") {
				continue
			}
			model := map[string]any{"id": entry.ID, "object": "model", "owned_by": "openai", "created": 0}
			if label := strings.TrimSpace(entry.Label); label != "" {
				model["display_name"] = label
			}
			efforts := make([]string, 0)
			effortSeen := map[string]bool{}
			for _, effort := range entry.Efforts {
				switch effort.Value {
				case "low", "medium", "high", "xhigh":
					if !effortSeen[effort.Value] {
						efforts = append(efforts, effort.Value)
						effortSeen[effort.Value] = true
					}
				}
			}
			if entry.Efforts != nil {
				model["reasoning_efforts"] = efforts
				model["supported_reasoning_levels"] = efforts
			}
			models = append(models, model)
			seen[entry.ID] = true
		}
	}
	return json.Marshal(map[string]any{"object": "list", "data": models})
}

// FetchExcelBPSModelsList uses a separate fresh-only cache scoped by bearer,
// account and proxy identity. Expired permissions are never served stale.
func (s *OpenAIGatewayService) FetchExcelBPSModelsList(ctx context.Context, account *Account) (*OpenAIModelsResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if account != nil {
		account = account.ExcelBPSCredentialAccount()
	}
	if s == nil || s.httpUpstream == nil || account == nil || !account.IsOpenAI() || !account.IsExcelOAuth() {
		return nil, fmt.Errorf("excel BPS model discovery is unavailable")
	}
	token, _, err := s.GetAccessToken(ctx, account)
	if err != nil {
		return nil, infraerrors.New(502, "EXCEL_BPS_MODELS_AUTH_UNAVAILABLE", "Excel BPS credentials are unavailable")
	}
	if err := validateExcelOAuthAccount(account, token); err != nil {
		return nil, infraerrors.New(502, "EXCEL_BPS_MODELS_AUTH_UNAVAILABLE", "Excel BPS credentials are unavailable")
	}
	accountID := excelOAuthAccountID(token)
	if accountID == "" {
		return nil, fmt.Errorf("excel BPS model discovery requires an account ID")
	}
	request, err := newExcelBPSRequest(ctx, nil, token, accountID)
	if err != nil {
		return nil, err
	}
	request.Method = http.MethodGet
	request.Body = nil
	request.GetBody = nil
	request.ContentLength = 0
	request.Header.Set("Accept", "application/json")
	request.URL.Path = "/basispoints/api/responses/access"
	request.URL.RawQuery = "include_models=true"
	proxy := upstreamModelsProxyURL(account)
	identity := openAIModelsRequest{url: request.URL.String(), headers: request.Header, proxyURL: proxy, accountID: account.ID, credentialAccountID: account.ID, standardModelsList: true}
	key := buildOpenAIModelsCacheKey(identity)
	if cached, state := s.excelBPSModelsCache.get(key, time.Now()); state == openAIModelsCacheFresh {
		return openAIModelsResponseForClient(cached, ""), nil
	}
	fetch := func(fetchCtx context.Context) (*OpenAIModelsResponse, error) {
		if cached, state := s.excelBPSModelsCache.get(key, time.Now()); state == openAIModelsCacheFresh {
			return cached, nil
		}
		fetchCtx, cancel := context.WithTimeout(fetchCtx, 5*time.Second)
		defer cancel()
		req := request.Clone(fetchCtx)
		response, err := s.doAccountRPMUpstream(req, proxy, account)
		if err != nil {
			if response != nil && response.Body != nil {
				_ = response.Body.Close()
			}
			return nil, infraerrors.New(502, "EXCEL_BPS_MODELS_UPSTREAM_FAILED", "Excel BPS model discovery request failed")
		}
		if response == nil || response.Body == nil {
			return nil, fmt.Errorf("excel BPS model discovery returned no response")
		}
		defer func() { _ = response.Body.Close() }()
		stop := context.AfterFunc(fetchCtx, func() { _ = response.Body.Close() })
		defer stop()
		if response.StatusCode != http.StatusOK {
			return nil, infraerrors.New(502, "EXCEL_BPS_MODELS_UPSTREAM_REJECTED", fmt.Sprintf("Excel BPS model discovery returned HTTP %d", response.StatusCode))
		}
		raw, err := io.ReadAll(io.LimitReader(response.Body, openAIModelsCacheBodyLimit+1))
		if err != nil || len(raw) > openAIModelsCacheBodyLimit {
			return nil, fmt.Errorf("excel BPS model catalog could not be read")
		}
		body, err := excelBPSAccessModelsBody(raw)
		if err != nil {
			return nil, err
		}
		if err := fetchCtx.Err(); err != nil {
			return nil, err
		}
		manifest := &OpenAIModelsResponse{Body: body, ETag: codexModelsManifestBodyETag(body)}
		s.excelBPSModelsCache.set(key, manifest, time.Now())
		return manifest, nil
	}
	ch := s.excelBPSModelsCache.refresh.DoChan(key, func() (any, error) { return fetch(context.Background()) })
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-ch:
		if result.Err != nil {
			return nil, result.Err
		}
		manifest, ok := result.Val.(*OpenAIModelsResponse)
		if !ok || manifest == nil {
			return nil, fmt.Errorf("excel BPS model discovery returned no catalog")
		}
		return openAIModelsResponseForClient(manifest, ""), nil
	}
}

// A Codex-compatible manifest is a projection of the discovered BPS IDs, never
// a native Codex request or a source of additional model permissions.
func (s *OpenAIGatewayService) fetchExcelBPSCodexManifest(ctx context.Context, account *Account, ifNoneMatch string) (*OpenAIModelsResponse, error) {
	access, err := s.FetchExcelBPSModelsList(ctx, account)
	if err != nil {
		return nil, err
	}
	_, entries, err := modelCatalogEntries(access.Body, "data")
	if err != nil {
		return nil, err
	}
	body := []byte(`{"models":[]}`)
	if len(entries) > 0 {
		// Conversion consumes upstream IDs. Account aliases are applied later.
		identity := *account
		identity.Credentials = make(map[string]any, len(account.Credentials))
		for key, value := range account.Credentials {
			if key != "model_mapping" {
				identity.Credentials[key] = value
			}
		}
		body = convertOpenAIModelListToCodexManifestForAccount(access.Body, &identity)
	}
	envelope, models, err := modelCatalogEntries(body, "models")
	if err != nil {
		return nil, err
	}
	byID := make(map[string]map[string]json.RawMessage, len(entries))
	for _, raw := range entries {
		var model map[string]json.RawMessage
		_ = json.Unmarshal(raw, &model)
		var id string
		_ = json.Unmarshal(model["id"], &id)
		byID[id] = model
	}
	for i, raw := range models {
		var model map[string]json.RawMessage
		_ = json.Unmarshal(raw, &model)
		var id string
		_ = json.Unmarshal(model["slug"], &id)
		capability := byID[id]
		if label := capability["display_name"]; label != nil {
			model["display_name"] = label
		}
		var efforts []string
		_ = json.Unmarshal(capability["reasoning_efforts"], &efforts)
		levels := make([]map[string]string, 0, len(efforts))
		for _, effort := range efforts {
			levels = append(levels, map[string]string{"effort": effort, "description": effort})
		}
		model["supported_reasoning_levels"], _ = json.Marshal(levels)
		model["default_reasoning_level"] = json.RawMessage("null")
		if len(efforts) > 0 {
			model["default_reasoning_level"], _ = json.Marshal(efforts[0])
		}
		for _, field := range []string{"prefer_websockets", "support_verbosity", "supports_reasoning_summary_parameter"} {
			model[field] = json.RawMessage("false")
		}
		model["multi_agent_reasoning_effort"] = json.RawMessage("null")
		models[i], _ = json.Marshal(model)
	}
	envelope["models"], _ = json.Marshal(models)
	body, err = json.Marshal(envelope)
	if err != nil {
		return nil, err
	}
	return openAIModelsResponseForClient(&OpenAIModelsResponse{Body: body, ETag: codexModelsManifestBodyETag(body)}, ifNoneMatch), nil
}

// Sync returns only live BPS permissions and metadata. Registry enrichment and
// configured IDs cannot add entitlements or override authoritative empty access.
func (s *AccountTestService) syncExcelBPSModelCatalog(ctx context.Context, account *Account) (*UpstreamModelCatalog, error) {
	if s == nil || s.openaiGatewayService == nil {
		return nil, newUpstreamModelSyncConfigError("BPS model discovery service is unavailable", nil)
	}
	response, err := s.openaiGatewayService.FetchExcelBPSModelsList(ctx, account)
	if err != nil {
		return nil, newUpstreamModelSyncUpstreamError("BPS model discovery failed", err)
	}
	var result struct {
		Data []struct {
			ID          string   `json:"id"`
			DisplayName string   `json:"display_name"`
			Efforts     []string `json:"reasoning_efforts"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body, &result); err != nil {
		return nil, newUpstreamModelSyncUpstreamError("BPS model catalog is invalid", err)
	}
	catalog := &UpstreamModelCatalog{Models: make([]string, 0, len(result.Data)), Metadata: make(map[string]UpstreamModelMetadata), DiscoverySource: "bps_access"}
	for _, entry := range result.Data {
		catalog.Models = append(catalog.Models, entry.ID)
		metadata := UpstreamModelMetadata{ID: entry.ID, DisplayName: entry.DisplayName, SupportedReasoningLevels: entry.Efforts}
		if len(entry.Efforts) > 0 {
			metadata.DefaultReasoningLevel = entry.Efforts[0]
		}
		catalog.Metadata[entry.ID] = metadata
	}
	return catalog, nil
}
