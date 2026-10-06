package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/prismbridge/prism"
)

func TestPrismEvalRequiresCompletedText(t *testing.T) {
	for _, status := range []*prism.StatusResponse{nil, {Text: "29"}, {Done: true, Fail: true, Text: "29"}, {Done: true, Error: "failed", Text: "29"}, {Done: true}} {
		if _, err := prismEvalSampleResponse(status, "actual-model"); err == nil {
			t.Fatal("unfinished or failed upstream response counted as a valid sample")
		}
	}
	result, err := prismEvalSampleResponse(&prism.StatusResponse{Done: true, Text: "29", Usage: &prism.Usage{InputTokens: 12, OutputTokens: 4}}, "actual-model")
	if err != nil || result.Text != "29" || result.Model != "actual-model" || result.InputTokens != 12 || result.OutputTokens != 4 {
		t.Fatalf("completed response: %+v, %v", result, err)
	}
}

func TestPrismEvalBaselineCoverage(t *testing.T) {
	if !prismEvalBaselineSupported(OpenAIEvalTypeCandy, "future-catalog-model") {
		t.Fatal("Candy semantics must support dynamic catalog text models")
	}
	for _, kind := range []string{OpenAIEvalTypeFingerprint, OpenAIEvalTypeModelTrace, OpenAIEvalTypeStateProbe} {
		if prismEvalBaselineSupported(kind, "future-catalog-model") {
			t.Fatalf("unmeasured model must not receive fabricated %s attribution", kind)
		}
	}
	if len(modelTraceBank.Models) > 0 && !prismEvalBaselineSupported(OpenAIEvalTypeModelTrace, modelTraceBank.Models[0].ID) {
		t.Fatal("versioned model bank must determine applicable coverage")
	}
}

func TestPrismEvalBaselineModelUsesVerifiedMappedTarget(t *testing.T) {
	target := &OpenAIEvalTarget{Account: &Account{Platform: PlatformPrism}, RequestedModel: "public-alias", UpstreamModel: "gpt-6.1-sol"}
	if openAIEvalBaselineModel(target, "public-alias") != "gpt-6.1-sol" {
		t.Fatal("public alias used as a nonexistent baseline")
	}
	target.Account.Platform = PlatformOpenAI
	if openAIEvalBaselineModel(target, "public-alias") != "public-alias" {
		t.Fatal("legacy baseline semantics changed")
	}
}
