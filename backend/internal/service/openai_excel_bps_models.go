package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// BPS has no native Codex discovery endpoint. Only explicit public mappings
// and concrete group selections describe its local catalog; subscription names
// and the global OpenAI defaults are not evidence of upstream model access.
func buildExcelBPSModelsManifest(account *Account, group *Group) (*OpenAIModelsResponse, error) {
	models := openAIConfiguredCodexModelIDs([]Account{*account})
	if group != nil && group.ModelAllowlistEnabled() {
		for _, selected := range group.ModelAllowlist.Models {
			selected = strings.TrimSpace(selected)
			if selected != "" && !strings.Contains(selected, "*") && account.IsModelSupported(selected) {
				models = append(models, selected)
			}
		}
	}
	if len(models) == 0 {
		return nil, infraerrors.New(http.StatusServiceUnavailable, "OPENAI_BPS_MODELS_NOT_CONFIGURED", "Configure explicit model mappings for the BPS account or select concrete models in the group model allowlist")
	}
	models = FilterCodexModelIDsForGroup(dedupeAndSortModelIDs(models), group)
	body, err := buildCodexModelsManifestForAccounts(PlatformOpenAI, models, []Account{*account}, group, nil, true)
	if err != nil {
		return nil, err
	}
	return &OpenAIModelsResponse{Body: body, ETag: codexModelsManifestBodyETag(body)}, nil
}

// FetchGroupAccountCodexModelsManifest preserves live discovery for native
// accounts and uses explicit local configuration for a BPS source. Projection
// happens exactly once: a generated BPS slug is already a public alias.
func (s *OpenAIGatewayService) FetchGroupAccountCodexModelsManifest(ctx context.Context, account *Account, group *Group, clientVersion string) (*OpenAIModelsResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if account.IsExcelOAuth() {
		return buildExcelBPSModelsManifest(account, group)
	}
	manifest, err := s.FetchCodexModelsManifest(ctx, account, clientVersion, "")
	if err != nil {
		return nil, err
	}
	if err := s.CompleteAPIKeyCodexModelsManifestForClient(manifest, account); err != nil {
		return nil, err
	}
	if err := ApplyPinnedCodexModelsMapping(manifest, account, group); err != nil {
		return nil, err
	}
	return manifest, nil
}

func buildExcelBPSModelsList(account *Account, group *Group) (*OpenAIModelsResponse, error) {
	manifest, err := buildExcelBPSModelsManifest(account, group)
	if err != nil {
		return nil, err
	}
	body, err := standardOpenAIModelsBody(manifest.Body, true)
	if err != nil {
		return nil, err
	}
	return &OpenAIModelsResponse{Body: body, ETag: codexModelsManifestBodyETag(body)}, nil
}

// Composite live manifests need the same BPS/native transport intersection as
// generated OpenAI catalogs. Do not apply OpenAI-only alias/retirement rules to
// other platforms or invent capabilities absent from the selected manifest.
func (s *OpenAIGatewayService) RestrictCompositeBPSModelsManifest(ctx context.Context, group *Group, manifest *OpenAIModelsResponse, resolver *CompositeRouteResolver, ifNoneMatch string) error {
	if group == nil || group.Platform != PlatformComposite || manifest == nil || manifest.NotModified || len(manifest.Body) == 0 {
		return nil
	}
	_, accounts, err := loadCodexGroupCatalogAccounts(ctx, s.accountRepo, group.ID)
	if err != nil {
		return fmt.Errorf("load composite BPS catalog accounts: %w", err)
	}
	hasBPS := false
	for i := range accounts {
		hasBPS = hasBPS || accounts[i].IsExcelOAuth()
	}
	if hasBPS {
		if resolver == nil || resolver.repo == nil {
			return fmt.Errorf("composite BPS catalog route resolver is unavailable")
		}
		routes, err := resolver.repo.ListByGroup(ctx, group.ID, false)
		if err != nil {
			return fmt.Errorf("load composite BPS catalog routes: %w", err)
		}
		body, changed, err := restrictExcelBPSModels(manifest.Body, PlatformComposite, accounts, group, routes, true)
		if err != nil {
			return fmt.Errorf("restrict composite BPS capabilities: %w", err)
		}
		if changed {
			manifest.Body = body
			manifest.ETag = codexModelsManifestBodyETag(body)
		}
	}
	if codexModelsManifestETagMatches(ifNoneMatch, manifest.ETag) {
		manifest.Body, manifest.NotModified = nil, true
	}
	return nil
}

// The owned BPS adapter implements plaintext v2 collaboration. Preserve the
// selected/group-intersected version declaration, including explicit null;
// native or pinned manifests must not force a reasoning tier onto BPS agents.
func restrictExcelBPSModels(body []byte, platform string, accounts []Account, group *Group, routes []CompositeModelRoute, routesAvailable bool) ([]byte, bool, error) {
	hasBPS := false
	for i := range accounts {
		hasBPS = hasBPS || accounts[i].IsExcelOAuth()
	}
	if !hasBPS {
		return body, false, nil
	}
	// Model discovery serves Responses. Routes for other endpoints do not
	// shadow account ownership or detector fallback in the actual resolver.
	if platform == PlatformComposite {
		if !routesAvailable {
			return nil, false, infraerrors.New(http.StatusServiceUnavailable, "CODEX_ROUTES_UNAVAILABLE", "Composite BPS routes are unavailable")
		}
		responseRoutes := make([]CompositeModelRoute, 0, len(routes))
		for _, route := range routes {
			endpoint := normalizeCompositeRouteEndpoint(route.Endpoint)
			if endpoint == CompositeRouteEndpointResponses || endpoint == CompositeRouteEndpointAny {
				responseRoutes = append(responseRoutes, route)
			}
		}
		routes = responseRoutes
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, false, err
	}
	var models []map[string]json.RawMessage
	if err := json.Unmarshal(envelope["models"], &models); err != nil {
		return nil, false, err
	}
	changed := false
	for _, model := range models {
		var slug string
		if json.Unmarshal(model["slug"], &slug) != nil {
			continue
		}
		slug = strings.TrimSpace(slug)
		target, targetPlatform := slug, platform
		ownershipRoute := false
		if platform == PlatformComposite {
			var resolved bool
			targetPlatform, target, resolved = resolveCodexCompositeModelTarget(slug, accounts, routes, routesAvailable)
			if !resolved {
				return nil, false, infraerrors.New(http.StatusServiceUnavailable, "CODEX_ROUTES_UNAVAILABLE", "Composite BPS model route cannot be determined")
			}
			if _, explicit := matchCompositeRoute(routes, slug, CompositeRouteEndpointResponses); !explicit {
				for i := range accounts {
					if accounts[i].Platform == targetPlatform && explicitModelMappingClaims(accounts[i], slug) {
						ownershipRoute = true
						break
					}
				}
			}
		}
		if targetPlatform != PlatformOpenAI {
			continue
		}
		routed := codexModelRoutingAccountIDs(group, slug)
		bpsRouted, allRoutedBPS := false, true
		for i := range accounts {
			a := &accounts[i]
			if !a.IsOpenAI() || len(routed) > 0 && !containsInt64(routed, a.ID) || !supportsOpenAIRequestModel(a, target, false) || !a.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityResponsesCompatible) || ownershipRoute && !explicitModelMappingClaims(*a, slug) {
				continue
			}
			bpsRouted = bpsRouted || a.IsExcelOAuth()
			allRoutedBPS = allRoutedBPS && a.IsExcelOAuth()
		}
		if !bpsRouted {
			continue
		}
		// A shared slug can reach BPS on any attempt. Its transport preference
		// must remain HTTP even when native accounts also serve the slug.
		for _, field := range []string{"prefer_websockets", "support_verbosity", "supports_reasoning_summary_parameter"} {
			if raw, exists := model[field]; exists && string(raw) != "false" {
				model[field] = json.RawMessage("false")
				changed = true
			}
		}
		fields := []string{"multi_agent_reasoning_effort"}
		if !allRoutedBPS {
			// Native accounts may produce encrypted agent history. Until all
			// eligible routes share the plaintext contract, use the portable
			// v1 protocol so failover cannot create unreadable BPS tasks.
			fields = append(fields, "multi_agent_version")
		}
		for _, field := range fields {
			if raw, exists := model[field]; exists && string(raw) != "null" {
				model[field] = json.RawMessage("null")
				changed = true
			}
		}
	}
	if !changed {
		return body, false, nil
	}
	var err error
	envelope["models"], err = json.Marshal(models)
	if err != nil {
		return nil, false, err
	}
	out, err := json.Marshal(envelope)
	return out, err == nil, err
}
