package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestModelTracePinnedBankStatisticalContract(t *testing.T) {
	digest := sha256.Sum256(modelTraceBankJSON)
	require.Equal(t, "a4e256c00444179b76f3855578660e66f30659df8c0122f1768dd05a8d705630", hex.EncodeToString(digest[:]))
	require.Len(t, modelTraceBank.Models, 17)
	counts := make(map[string][]float64)
	finiteVector := func(values []float64, dimension int) {
		t.Helper()
		require.Len(t, values, dimension)
		for _, v := range values {
			require.False(t, math.IsNaN(v) || math.IsInf(v, 0))
		}
	}
	for _, model := range modelTraceBank.Models {
		require.NotContains(t, counts, model.ID)
		finiteVector(model.Counts, 355)
		total := 0.0
		for _, n := range model.Counts {
			require.GreaterOrEqual(t, n, 0.0)
			total += n
		}
		require.Positive(t, total)
		counts[model.ID] = model.Counts
	}
	require.Contains(t, counts, "gpt-6.1-sol")
	require.Contains(t, counts, "gpt-6-sol")
	require.NotEqual(t, counts["gpt-6-sol"], counts["gpt-6.1-sol"], "new identity needs its own measured profile")
	for name, features := range map[string]traceFeatures{"hellinger": modelTraceBank.Robust.Hellinger, "ordered": modelTraceBank.Robust.Ordered} {
		dimension := 355
		if name == "ordered" {
			dimension = 74
			require.NotEmpty(t, features.Environments)
		}
		finiteVector(features.Mean, dimension)
		finiteVector(features.Scale, dimension)
		for _, scale := range features.Scale {
			require.Positive(t, scale)
		}
		require.Len(t, features.Centroids, len(modelTraceBank.Models))
		for _, c := range features.Centroids {
			finiteVector(c, dimension)
		}
		for _, basis := range features.Basis {
			finiteVector(basis, dimension)
		}
		for _, env := range features.Environments {
			require.Len(t, env, len(modelTraceBank.Models))
			for _, c := range env {
				finiteVector(c, dimension)
			}
		}
	}
	for i := 1; i <= 3; i++ {
		beta := modelTraceBank.Calibration[strconv.Itoa(i)].Beta
		require.Positive(t, beta)
		require.False(t, math.IsNaN(beta) || math.IsInf(beta, 0))
	}
}

func TestModelTracePinnedUpstreamGoldenParity(t *testing.T) {
	raw, err := os.ReadFile("data/modeltrace/modeltrace-golden_97623969.json")
	require.NoError(t, err)
	var cases []struct {
		Outputs  []traceOutput    `json:"outputs"`
		Expected traceAttribution `json:"expected"`
	}
	require.NoError(t, json.Unmarshal(raw, &cases))
	require.Len(t, cases, 3)
	for _, tc := range cases {
		t.Run(fmt.Sprintf("outputs_%d", tc.Expected.Used), func(t *testing.T) {
			got, err := analyzeModelTrace(tc.Outputs)
			require.NoError(t, err)
			want := tc.Expected
			require.Equal(t, want.Prediction, got.Prediction)
			require.Equal(t, want.Used, got.Used)
			require.Equal(t, want.Diagnostics, got.Diagnostics)
			require.Len(t, got.Results, len(want.Results))
			require.Len(t, got.Families, len(want.Families))
			require.InDelta(t, want.Probability, got.Probability, 1e-10)
			require.Equal(t, want.FamilyPrediction, got.FamilyPrediction)
			require.InDelta(t, want.FamilyProbability, got.FamilyProbability, 1e-10)
			total := 0.0
			for i, result := range got.Results {
				require.Equal(t, want.Results[i].Model, result.Model)
				require.Equal(t, want.Results[i].Family, result.Family)
				require.InDelta(t, want.Results[i].Probability, result.Probability, 1e-10)
				require.InDelta(t, want.Results[i].Score, result.Score, 1e-10)
				require.InDelta(t, want.Results[i].Similarity, result.Similarity, 1e-10)
				total += result.Probability
			}
			require.InDelta(t, 1.0, total, 1e-12)
			for i, family := range got.Families {
				require.Equal(t, want.Families[i].Family, family.Family)
				require.InDelta(t, want.Families[i].Probability, family.Probability, 1e-10)
			}
		})
	}
}

func TestFingerprintPinnedDataModelsAndProbeCompatibility(t *testing.T) {
	raw, err := os.ReadFile("data/cpa_fingerprint_probes_97623969.json")
	require.NoError(t, err)
	old, err := os.ReadFile("data/cpa_fingerprint_probes_5654020c.json")
	require.NoError(t, err)
	require.Equal(t, old, raw, "the pinned baselines use the existing probe contract")
	var probes []OpenAIEvalProbe
	require.NoError(t, json.Unmarshal(raw, &probes))
	probeByID := make(map[string]OpenAIEvalProbe)
	for _, probe := range probes {
		probeByID[probe.ID] = probe
	}
	raw, err = os.ReadFile("data/cpa_fingerprint_baselines_97623969.json")
	require.NoError(t, err)
	digest := sha256.Sum256(raw)
	require.Equal(t, "bf3a8f19be9206827a541489ea4e6542010aedd4d4f4c3b4b19438e2ae0a6161", hex.EncodeToString(digest[:]))
	var baselines []OpenAIEvalFingerprintBaseline
	require.NoError(t, json.Unmarshal(raw, &baselines))
	models := make(map[string]OpenAIEvalFingerprintBaseline)
	for _, baseline := range baselines {
		require.NotContains(t, models, baseline.Model)
		models[baseline.Model] = baseline
		require.Len(t, baseline.Cells, len(probes))
		for cell, answers := range baseline.Cells {
			probe, ok := probeByID[cell]
			require.True(t, ok, "unknown cell %s", cell)
			// Upstream retains only valid samples: one Terra cell has 24.
			require.GreaterOrEqual(t, len(answers), 10)
			require.LessOrEqual(t, len(answers), 25)
			for _, answer := range answers {
				_, valid := NormalizeOpenAIEvalFingerprintAnswer(answer, probe)
				require.True(t, valid, "invalid baseline model=%s probe=%s", baseline.Model, cell)
			}
		}
	}
	require.Len(t, models, 8)
	require.Contains(t, models, "gpt-6.1-sol")
	require.NotEqual(t, models["gpt-6-sol"].Cells, models["gpt-6.1-sol"].Cells)
	samples := make([]OpenAIEvalSample, 0, 400)
	for cell, answers := range models["gpt-6.1-sol"].Cells {
		for _, answer := range answers {
			samples = append(samples, OpenAIEvalSample{ProbeID: cell, Answer: answer})
		}
	}
	result := ScoreOpenAIEvalFingerprint("gpt-6.1-sol", samples, baselines, 400)
	require.Equal(t, "gpt-6.1-sol", result.NearestModel)
	require.Equal(t, 400, result.ValidSamples)
	require.NotNil(t, result.MeanJSD)
	require.InDelta(t, 0.0, *result.MeanJSD, 1e-12)
}
