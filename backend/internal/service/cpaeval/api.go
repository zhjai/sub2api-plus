// Package cpaeval ports the pure evaluation logic of
// haowang02/cpa-plugin-codex-candy-eval v0.4.4. See README.md and LICENSE.
package cpaeval

import (
	"encoding/json"
	"slices"
	"time"
)

const (
	SourceRevision               = "179d6dae48a0f0a735637ea2f373345b91caaca8"
	FingerprintTarget            = fingerprintTarget
	FingerprintGlobalConcurrency = fingerprintGlobalConcurrency
	FingerprintMaxText           = fingerprintMaxText
	CandyPrompt                  = candyPrompt
	PelicanMaxCode               = pelicanMaxCode
)

type FingerprintChallenge = fingerprintChallenge
type FingerprintOutput = fingerprintOutput
type FingerprintAttribution = fingerprintAttribution
type FingerprintCandidate = fingerprintCandidate
type FingerprintDiagnostic = fingerprintDiagnostic
type FingerprintSequence = fingerprintSequence
type Model = detectorModel
type PelicanVariant = pelicanVariant

// FingerprintChallenges draws three distinct challenges from the enrollment suite.
func FingerprintChallenges() []FingerprintChallenge { return fingerprintChallenges() }

func FingerprintSuite() []FingerprintChallenge { return slices.Clone(fingerprintSuite) }

// FingerprintText validates and truncates an answer for a suite challenge. Pass
// its returned text, including invalid answers, to AnalyzeFingerprint so the
// attribution retains diagnostics in challenge order. expected comes from the suite.
func FingerprintText(answer string, expected int) (string, int, error) {
	return fingerprintText(answer, expected)
}

func AnalyzeFingerprint(outputs []FingerprintOutput) (FingerprintAttribution, error) {
	return analyzeFingerprint(outputs)
}

// FingerprintModelID normalizes comparison candidates only. Never use it to
// rewrite a routed/requested model.
func FingerprintModelID(model string) string { return fingerprintModelID(model) }

func ModelEnrolled(model string) bool {
	return slices.Contains(sharedDetector.ModelIDs, fingerprintModelID(model))
}

func Models() []Model { return slices.Clone(sharedDetector.models) }

func FingerprintAliases() map[string]string {
	aliases := make(map[string]string, len(fingerprintAliases))
	for model, candidate := range fingerprintAliases {
		aliases[model] = candidate
	}
	return aliases
}

func DetectorRevision() string                              { return detectorRevision }
func DetectorLicense() string                               { return detectorLicense }
func HasStandalone21(text string) bool                      { return hasStandalone21(text) }
func PelicanVariants() []PelicanVariant                     { return slices.Clone(pelicanVariants) }
func FindPelicanVariant(id string) (PelicanVariant, bool)   { return findPelicanVariant(id) }
func (v pelicanVariant) Page(answer string) (string, error) { return v.page(answer) }
func (v pelicanVariant) Language() string                   { return v.language }
func ExtractCode(answer, language string) string            { return extractCode(answer, language) }

// NewCodexPayload builds a fresh upstream turn with the caller's stable
// installation identity and clock. It preserves explicit effort values (including
// "none") and the actual model. OAuth projection and headers belong to the caller.
// Each returned map is independently mutable, including its RawMessage tools.
func NewCodexPayload(installationID, model, effort, instructions, prompt string, now time.Time) (map[string]any, string, error) {
	request, _ := codexTurnWithInstallationAt(installationID, model, effort, instructions, prompt, now)
	body, err := json.Marshal(request)
	if err != nil {
		return nil, "", err
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, "", err
	}
	// Keep the sent tools and the prefix ID hash sourced from the same compact
	// fixture bytes; decoding them into maps would re-encode those bytes.
	payload["input"].([]any)[0].(map[string]any)["tools"] = json.RawMessage(slices.Clone(codexTools))
	return payload, request.PromptCacheKey, nil
}
