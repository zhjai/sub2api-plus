package cpaeval

import (
	"encoding/json"
	"math"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// fingerprintAnswer is a gpt-6.1-sol reference answer from the golden cases, so it moves like a model's answer.
var fingerprintAnswer = func() string {
	raw, err := os.ReadFile("testdata/lm-detector-golden.json")
	if err != nil {
		panic(err)
	}
	return mustDecode[[]struct{ Outputs []fingerprintOutput }](raw)[0].Outputs[0].Text
}()

func TestFingerprintUpstreamParity(t *testing.T) {
	// Golden results come from the pinned upstream analyzeSharedOutputs with allowPartial, as its page runs it:
	// calibrated rankings (one for kimi-k3.1-pro answers whose small-step share alone exceeds its limit),
	// a partial ranking and answers that do not move like a model's.
	raw, err := os.ReadFile("testdata/lm-detector-golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Outputs  []fingerprintOutput
		Expected fingerprintAttribution
	}
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	close := func(a, b *float64) {
		t.Helper()
		if (a == nil) != (b == nil) || a != nil && !(math.Abs(*a-*b) <= 1e-9) {
			t.Fatalf("got %v, want %v", a, b)
		}
	}
	for _, tt := range cases {
		got, err := analyzeFingerprint(tt.Outputs)
		if err != nil {
			t.Fatal(err)
		}
		want := tt.Expected
		if got.Decision != want.Decision || got.Prediction != want.Prediction || got.PredictionName != want.PredictionName || got.FamilyPrediction != want.FamilyPrediction ||
			got.Used != want.Used || got.Sequence.ModelLike != want.Sequence.ModelLike || !reflect.DeepEqual(got.Diagnostics, want.Diagnostics) || len(got.Results) != len(want.Results) {
			t.Fatalf("attribution mismatch: %+v", got)
		}
		close(got.Probability, want.Probability)
		close(&got.Sequence.Autocorrelation, &want.Sequence.Autocorrelation)
		close(&got.Sequence.SmallStepShare, &want.Sequence.SmallStepShare)
		for i, r := range got.Results {
			w := want.Results[i]
			if r.Model != w.Model || r.DisplayName != w.DisplayName || r.Family != w.Family || r.FamilyName != w.FamilyName {
				t.Fatalf("rank %d: %+v, want %+v", i, r, w)
			}
			close(r.Probability, w.Probability)
			close(&r.Score, &w.Score)
		}
	}
}

func TestFingerprintParseAndChallenges(t *testing.T) {
	for text, want := range map[string][]int{
		"生成 300 个：1, 22, 355, 0, 356, 99。完成 4": {1, 22, 355, 99},
		"1,2 words 3,4": {1, 2},
		"1,2 中文 3,4,5":  {3, 4, 5},
		"1,2 😀 3,4":     {1, 2, 3, 4},
	} {
		if got := parseNumbers(text); !reflect.DeepEqual(got, want) {
			t.Fatalf("parse %q = %v, want %v", text, got, want)
		}
	}
	if _, err := analyzeFingerprint([]fingerprintOutput{{strings.Repeat("42,", 164), 300}}); err == nil {
		t.Fatal("short output was accepted")
	}
	if got, err := analyzeFingerprint([]fingerprintOutput{{strings.Repeat("42,", 165), 300}}); err != nil || got.Used != 1 || got.Decision != "not_model" || got.Results != nil {
		t.Fatalf("repeated numbers = %+v, %v", got, err)
	}
	instructed, prefixed := 0, 0
	for _, c := range fingerprintSuite {
		if c.Instructions != "" {
			instructed++
		}
		if strings.Contains(c.Prompt, "\n\nFinal task:\n") {
			prefixed++
		}
	}
	if len(fingerprintSuite) != 36 || instructed != 12 || prefixed != 9 {
		t.Fatalf("suite: %d challenges, %d with instructions, %d with a user prefix", len(fingerprintSuite), instructed, prefixed)
	}
	seen := map[string]bool{}
	for _, c := range fingerprintChallenges() {
		if seen[c.Prompt] || !slices.Contains(fingerprintSuite, c) {
			t.Fatalf("bad challenge: %+v", c)
		}
		seen[c.Prompt] = true
	}
	if fingerprintModelID("openai/GPT-6.1-sol") != "gpt-6-astra" || fingerprintModelID("deepseek/deepseek-flash") != "deepseek-v4.1-flash-1017" || fingerprintModelID("gpt-6-sol") != "gpt-6-sol" {
		t.Fatal("aliased models must compare as their candidates")
	}
}
