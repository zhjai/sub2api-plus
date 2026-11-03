package cpaeval

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestExportedFingerprintContract(t *testing.T) {
	for model, want := range map[string]bool{"openai/GPT-6.1-sol": true, "deepseek/deepseek-flash": true, "not-enrolled": false} {
		if ModelEnrolled(model) != want {
			t.Fatalf("enrollment %q", model)
		}
	}
	models := Models()
	models[0].ID = "changed"
	if Models()[0].ID == "changed" {
		t.Fatal("mutable model bank escaped")
	}
	suite := FingerprintSuite()
	suite[0].Prompt = "changed"
	if FingerprintSuite()[0].Prompt == "changed" {
		t.Fatal("mutable suite escaped")
	}
	aliases := FingerprintAliases()
	aliases["gpt-6.1-sol"] = "changed"
	if FingerprintModelID("gpt-6.1-sol") != "gpt-6-astra" {
		t.Fatal("mutable aliases escaped")
	}
	if len(FingerprintChallenges()) != 3 || !strings.HasPrefix(DetectorRevision(), "sha256:") || DetectorLicense() == "" {
		t.Fatal("missing detector contract")
	}
	for _, tc := range []struct {
		answer string
		count  int
		ok     bool
	}{
		{strings.Repeat("42,", 164), 164, false},
		{strings.Repeat("42,", 165), 165, true},
		{strings.Repeat("42,", 320), 300, true},
		{strings.Repeat("x", FingerprintMaxText+1), 0, false},
	} {
		text, parsed, err := FingerprintText(tc.answer, 300)
		if parsed != tc.count || (err == nil) != tc.ok {
			t.Fatalf("validation count=%d err=%v", parsed, err)
		}
		attr, analysisErr := AnalyzeFingerprint([]FingerprintOutput{{text, 300}})
		if tc.ok && (analysisErr != nil || attr.Used != 1 || attr.Decision != "not_model") {
			t.Fatalf("analysis=%+v err=%v", attr, analysisErr)
		}
		if !tc.ok && analysisErr == nil {
			t.Fatal("invalid answer scored")
		}
	}
}

func TestExportedPelicanContract(t *testing.T) {
	if !strings.HasSuffix(PelicanPreviewFreeze, "</script>") || strings.Contains(PelicanPreviewFreeze, `<\/script>`) {
		t.Fatal("preview must contain the evaluated JavaScript string, with a real closing script tag")
	}
	for _, variant := range PelicanVariants() {
		found, ok := FindPelicanVariant(variant.ID)
		if !ok || found != variant || !strings.HasPrefix(variant.Prompt, pelicanPreamble) {
			t.Fatal("variant mismatch")
		}
		page, err := variant.Page("```" + variant.Language() + "\n<svg/>\n```")
		if err != nil || !strings.HasSuffix(page, "<svg/>") {
			t.Fatalf("page=%q err=%v", page, err)
		}
		if variant.Language() == "svg" && !strings.Contains(page, "body > svg") {
			t.Fatal("SVG page scaling missing")
		}
		if _, err := variant.Page("refusal"); err == nil {
			t.Fatal("refusal became a page")
		}
		if _, err := variant.Page("<" + strings.Repeat("x", PelicanMaxCode)); err == nil {
			t.Fatal("oversized page accepted")
		}
	}
	if _, ok := FindPelicanVariant("unknown"); ok {
		t.Fatal("unknown variant accepted")
	}
}

func TestCodexPayloadWireAndIsolation(t *testing.T) {
	const fixtureHash = "65f976b3a7cb410e50312e5b4096686e5b1fda3e4fa1942ee3974d7cb1e7cd42"
	if fmt.Sprintf("%x", sha256.Sum256(codexContextJSON)) != fixtureHash {
		t.Fatal("fixture changed")
	}
	serviceFixture, err := os.ReadFile("../data/codex_eval/codex_context.json")
	if err != nil || !bytes.Equal(serviceFixture, codexContextJSON) {
		t.Fatalf("service fixture diverged: %v", err)
	}
	now := time.Date(2026, 11, 2, 3, 4, 5, 0, time.UTC)
	var wg sync.WaitGroup
	seen := sync.Map{}
	for range 8 {
		wg.Go(func() {
			payload, session, err := NewCodexPayload("stable-installation", "gpt-6.1-sol", "none", "dev <tag>", "question <tag>", now)
			if err != nil {
				t.Error(err)
				return
			}
			if _, loaded := seen.LoadOrStore(session, true); loaded {
				t.Error("reused session")
			}
			var body bytes.Buffer
			encoder := json.NewEncoder(&body)
			encoder.SetEscapeHTML(false)
			if err := encoder.Encode(payload); err != nil {
				t.Error(err)
				return
			}
			var wire codexRequest
			if err := json.Unmarshal(body.Bytes(), &wire); err != nil {
				t.Error(err)
				return
			}
			var meta codexTurnMetadata
			if err := json.Unmarshal([]byte(wire.ClientMetadata.TurnMetadata), &meta); err != nil {
				t.Error(err)
				return
			}
			if wire.Model != "gpt-6.1-sol" || wire.Reasoning.Effort != "none" || wire.ClientMetadata.InstallationID != "stable-installation" || meta.InstallationID != "stable-installation" || meta.HistoryInitialization != "new" || meta.TurnStartedAtUnixMS != now.UnixMilli() {
				t.Error("request contract changed")
			}
			if wire.Input[2].Content[0].Text != "dev <tag>" || !reflect.DeepEqual(wire.Input[2].Content[1:], codexMessage("", "", "", 0, codexContext.Messages[0].Content...).Content) || wire.Input[len(wire.Input)-1].Content[0].Text != "question <tag>" {
				t.Error("prompt or context changed")
			}
			if !bytes.Equal(wire.Input[0].Tools, codexTools) {
				t.Error("wire tools differ from hash source")
			}
			for _, id := range []string{session, meta.TurnID, meta.ContextWindowID} {
				if len(id) != 36 || id[14] != '7' {
					t.Errorf("not UUIDv7: %q", id)
				}
			}
			for _, item := range wire.Input[2:] {
				if item.ID[18] != '7' || item.Metadata.TurnID != meta.TurnID || item.Metadata.CreateTime != float64(now.UnixMicro())/1e6 {
					t.Error("message identity changed")
				}
			}
			payload["input"].([]any)[0].(map[string]any)["tools"].(json.RawMessage)[0] = '!'
		})
	}
	wg.Wait()
	if codexTools[0] != '[' {
		t.Fatal("shared tools mutated")
	}
}
