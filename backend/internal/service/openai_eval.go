package service

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"math"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
)

const (
	OpenAIEvalTypeCandy                  = "candy"
	OpenAIEvalTypeFingerprint            = "fingerprint"
	OpenAIEvalTypeModelTrace             = "modeltrace"
	OpenAIEvalDataVersion                = "cpa-codex-candy-eval-5654020c-v1"
	OpenAIEvalBaselineVersion            = "cpa-codex-candy-eval-5654020c-v1"
	OpenAIEvalMinFingerprintInterval     = 24 * time.Hour
	OpenAIEvalFingerprintQuickSamples    = 60
	OpenAIEvalFingerprintStandardSamples = 200
	OpenAIEvalFingerprintStrictSamples   = 400
	openAIEvalFingerprintQuickCells      = 4
	openAIEvalFingerprintStandardCells   = 8
	openAIEvalFingerprintStrictCells     = 16
	openAIEvalFingerprintQuickRepeats    = 15
	openAIEvalFingerprintStandardRepeats = 25
	openAIEvalFingerprintStrictRepeats   = 25
	openAIEvalFingerprintPermutationN    = 1000
	OpenAIEvalModelTraceRequests         = 3
	OpenAIEvalModelTraceMinInterval      = 24 * time.Hour
)

// Keep the Candy canary compatible with the upstream CPA plugin contract.
// The prompt and data version are pinned so historical runs remain comparable.
const OpenAIEvalCandyExpectedAnswer = 21

// CPA Candy/Fingerprint probe and reference data are vendored from
// haowang02/cpa-plugin-codex-candy-eval at commit 5654020c1815b4c4d29139fa76b1fbfcffd9a990.
// The upstream MIT license and copyright notice are kept beside these data files.
//
//go:embed data/cpa_fingerprint_probes_5654020c.json
var openAIEvalFingerprintProbesJSON []byte

//go:embed data/cpa_fingerprint_baselines_5654020c.json
var openAIEvalFingerprintBaselinesJSON []byte

var OpenAIEvalFingerprintProbes = mustDecodeOpenAIEvalJSON[[]OpenAIEvalProbe](openAIEvalFingerprintProbesJSON)
var OpenAIEvalFingerprintBaselines = mustDecodeOpenAIEvalJSON[[]OpenAIEvalFingerprintBaseline](openAIEvalFingerprintBaselinesJSON)

func mustDecodeOpenAIEvalJSON[T any](data []byte) T {
	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		panic(err)
	}
	return value
}

// OpenAIEvalOutcome is a route-scoped diagnostic. Fingerprint identity
// evidence is intentionally separate from capability outcomes.
type OpenAIEvalOutcome struct {
	Status        string                       `json:"status"`
	Reason        string                       `json:"reason,omitempty"`
	Score         float64                      `json:"score,omitempty"`
	SampleCount   int                          `json:"sample_count"`
	ExpectedCount int                          `json:"expected_count"`
	Confidence    string                       `json:"confidence"`
	Scheduling    string                       `json:"scheduling"`
	Fingerprint   *OpenAIEvalFingerprintResult `json:"fingerprint,omitempty"`
	ModelTrace    *OpenAIEvalModelTraceResult  `json:"modeltrace,omitempty"`
}

// OpenAIEvalRouteHealth is scoped to account + public requested model +
// reasoning effort. It is not an account-wide model-quality verdict.
type OpenAIEvalRouteHealth struct {
	AccountID         int64     `json:"account_id"`
	RequestedModel    string    `json:"requested_model"`
	ReasoningEffort   string    `json:"reasoning_effort"`
	HardFailureStreak int       `json:"hard_failure_streak"`
	LastFailureCode   string    `json:"last_failure_code,omitempty"`
	Penalty           float64   `json:"penalty"`
	PenaltyUntil      time.Time `json:"penalty_until,omitempty"`
	UpdatedAt         time.Time `json:"updated_at"`
}

const OpenAIEvalRouteHealthTTL = 30 * time.Minute

var openAIEvalEffectsEnabled atomic.Bool

func OpenAIEvalEffectsEnabled() bool { return openAIEvalEffectsEnabled.Load() }

func SetOpenAIEvalEffectsEnabled(enabled bool) { openAIEvalEffectsEnabled.Store(enabled) }

func OpenAIEvalRouteHealthKey(model, effort string) string {
	return strings.ToLower(strings.TrimSpace(model)) + "\x00" + strings.ToLower(strings.TrimSpace(effort))
}

func OpenAIEvalRouteHealthExtraKeyFor(model, effort string) string {
	digest := sha256.Sum256([]byte(OpenAIEvalRouteHealthKey(model, effort)))
	return "openai_eval_route_health_" + hex.EncodeToString(digest[:8])
}

func ReadOpenAIEvalRouteHealthFromAccount(account *Account, model, effort string, now time.Time) (OpenAIEvalRouteHealth, bool) {
	if account == nil || account.Extra == nil {
		return OpenAIEvalRouteHealth{}, false
	}
	value, ok := account.Extra[OpenAIEvalRouteHealthExtraKeyFor(model, effort)]
	if !ok {
		return OpenAIEvalRouteHealth{}, false
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return OpenAIEvalRouteHealth{}, false
	}
	var health OpenAIEvalRouteHealth
	if json.Unmarshal(encoded, &health) != nil || health.Penalty <= 0 || health.UpdatedAt.IsZero() {
		return OpenAIEvalRouteHealth{}, false
	}
	if !health.PenaltyUntil.IsZero() && now.After(health.PenaltyUntil) {
		return OpenAIEvalRouteHealth{}, false
	}
	return health, true
}

type OpenAIEvalRun struct {
	ID              int64                    `json:"id"`
	AccountID       int64                    `json:"account_id"`
	TestType        string                   `json:"test_type"`
	RequestedModel  string                   `json:"requested_model"`
	UpstreamModel   string                   `json:"upstream_model,omitempty"`
	ReasoningEffort string                   `json:"reasoning_effort"`
	DataVersion     string                   `json:"data_version"`
	BaselineVersion string                   `json:"baseline_version"`
	Status          string                   `json:"status"`
	Outcome         OpenAIEvalOutcome        `json:"outcome"`
	RequestCount    int                      `json:"request_count"`
	InputTokens     int64                    `json:"input_tokens"`
	OutputTokens    int64                    `json:"output_tokens"`
	CostEstimateUSD *float64                 `json:"cost_estimate_usd"`
	DurationMS      int64                    `json:"duration_ms"`
	StartedAt       time.Time                `json:"started_at"`
	FinishedAt      time.Time                `json:"finished_at"`
	TriggeredBy     int64                    `json:"triggered_by,omitempty"`
	TriggerSource   string                   `json:"trigger_source"`
	Error           string                   `json:"error,omitempty"`
	Samples         []OpenAIEvalSampleRecord `json:"samples,omitempty"`
}

type OpenAIEvalSampleRecord struct {
	ProbeID          string `json:"probe_id"`
	NormalizedAnswer string `json:"normalized_answer,omitempty"`
	Valid            bool   `json:"valid"`
	ErrorCode        string `json:"error_code,omitempty"`
}

type OpenAIEvalRunFilter struct {
	AccountID       int64
	RequestedModel  string
	ReasoningEffort string
	TestType        string
	Limit           int
}

type OpenAIEvalScheduledRun struct {
	AccountID       int64
	TestType        string
	RequestedModel  string
	ReasoningEffort string
	SampleMode      string
}

type OpenAIEvalAuditEvent struct {
	ID        int64          `json:"id"`
	ActorID   int64          `json:"actor_id"`
	Action    string         `json:"action"`
	Payload   map[string]any `json:"payload"`
	CreatedAt time.Time      `json:"created_at"`
}

type OpenAIEvalRepository interface {
	GetConfig(context.Context) (*OpenAIEvalConfig, error)
	SaveConfig(context.Context, *OpenAIEvalConfig, int64) error
	CreateRun(context.Context, *OpenAIEvalRun) (int64, error)
	FinishRun(context.Context, int64, *OpenAIEvalRun) error
	ListRuns(context.Context, OpenAIEvalRunFilter) ([]OpenAIEvalRun, error)
	ListAuditEvents(context.Context, int) ([]OpenAIEvalAuditEvent, error)
	ClaimDueSchedules(context.Context, time.Time, int) ([]OpenAIEvalScheduledRun, error)
	AcquireLease(context.Context, string, string, time.Duration) (bool, error)
	RenewLease(context.Context, string, string, time.Duration) (bool, error)
	ReleaseLease(context.Context, string, string) error
}

type OpenAIEvalSchedule struct {
	Enabled         bool       `json:"enabled"`
	IntervalSeconds int        `json:"interval_seconds"`
	JitterSeconds   int        `json:"jitter_seconds"`
	SampleMode      string     `json:"sample_mode,omitempty"`
	LastRunAt       *time.Time `json:"last_run_at,omitempty"`
	NextRunAt       *time.Time `json:"next_run_at,omitempty"`
}

type OpenAIEvalAccountConfig struct {
	AccountID           int64              `json:"account_id"`
	RequestedModel      string             `json:"requested_model"`
	ReasoningEffort     string             `json:"reasoning_effort"`
	CandySchedule       OpenAIEvalSchedule `json:"candy_schedule"`
	FingerprintSchedule OpenAIEvalSchedule `json:"fingerprint_schedule"`
	ModelTraceSchedule  OpenAIEvalSchedule `json:"modeltrace_schedule"`
}

type OpenAIEvalConfig struct {
	EffectsEnabled bool                      `json:"effects_enabled"`
	Accounts       []OpenAIEvalAccountConfig `json:"accounts"`
}

type OpenAIEvalProbe struct {
	ID           string   `json:"id"`
	Kind         string   `json:"kind"`
	Low          int      `json:"lo"`
	High         int      `json:"hi"`
	Instructions string   `json:"instructions"`
	Prompts      []string `json:"prompts"`
}

type OpenAIEvalFingerprintBaseline struct {
	Model string              `json:"model"`
	Cells map[string][]string `json:"cells"`
}

type OpenAIEvalFingerprintResult struct {
	Status          string    `json:"status"`
	NearestModel    string    `json:"nearest_model,omitempty"`
	MeanJSD         *float64  `json:"mean_jsd,omitempty"`
	SelfJSD         *float64  `json:"self_jsd,omitempty"`
	PValue          *float64  `json:"p_value,omitempty"`
	ValidSamples    int       `json:"valid_samples"`
	RequiredSamples int       `json:"required_samples"`
	CellCount       int       `json:"cell_count"`
	Reason          string    `json:"reason,omitempty"`
	EvaluatedAt     time.Time `json:"evaluated_at"`
}

type OpenAIEvalModelTraceCandidate struct {
	Model       string  `json:"model"`
	DisplayName string  `json:"display_name"`
	Family      string  `json:"family"`
	FamilyName  string  `json:"family_name"`
	Probability float64 `json:"probability"`
	Similarity  float64 `json:"profile_similarity"`
	Score       float64 `json:"score"`
}

type OpenAIEvalModelTraceFamily struct {
	Family      string  `json:"family"`
	DisplayName string  `json:"display_name"`
	Probability float64 `json:"probability"`
}

type OpenAIEvalModelTraceDiagnostic struct {
	Index   int  `json:"index"`
	Parsed  int  `json:"parsed_numbers"`
	Minimum int  `json:"minimum_numbers"`
	Valid   bool `json:"accepted"`
}

type OpenAIEvalModelTraceSample struct {
	Prompt        string `json:"-"`
	ExpectedCount int    `json:"expected_count"`
	Attempts      int    `json:"attempts,omitempty"`
	Text          string `json:"-"`
	Error         string `json:"error,omitempty"`
	Parsed        int    `json:"parsed_numbers"`
	Valid         bool   `json:"accepted"`
}

type OpenAIEvalModelTraceResult struct {
	BankRevision      string                           `json:"bank_revision"`
	Prediction        string                           `json:"prediction,omitempty"`
	Probability       float64                          `json:"probability,omitempty"`
	FamilyPrediction  string                           `json:"family_prediction_name,omitempty"`
	FamilyProbability float64                          `json:"family_probability,omitempty"`
	UsedOutputs       int                              `json:"used_outputs"`
	Requests          int                              `json:"requests"`
	Candidates        []OpenAIEvalModelTraceCandidate  `json:"candidates,omitempty"`
	Families          []OpenAIEvalModelTraceFamily     `json:"families,omitempty"`
	Diagnostics       []OpenAIEvalModelTraceDiagnostic `json:"diagnostics,omitempty"`
	Samples           []OpenAIEvalModelTraceSample     `json:"samples,omitempty"`
}

type OpenAIEvalSample struct {
	ProbeID string
	Answer  string
	Error   string
}

// OpenAIEvalTarget is resolved once per run so a multi-sample fingerprint
// consistently uses one account and one upstream model mapping.
type OpenAIEvalTarget struct {
	Account        *Account
	Credential     *Account
	RequestedModel string
	UpstreamModel  string
}

type OpenAIEvalSampleResponse struct {
	Text         string
	InputTokens  int64
	OutputTokens int64
	CompletedAt  time.Time
}

// OpenAIEvalCandyPrompt is the canonical CPA-compatible public canary. Keep
// this text stable: the expected answer and the vendored fingerprint data are
// versioned together, so changing the wording changes the evaluation.
var OpenAIEvalCandyPrompt = `不使用任何外部工具回答以下问题：

在一个黑色的袋子里放有三种口味的糖果，每种糖果有两种不同的形状（圆形和五角星形，不同的形状靠手感可以分辨）。参赛者需要在活动前决定摸出的糖果数目，不能通过触摸辨别口味。那么，最少取出多少个糖果才能保证手中同时拥有不同形状的苹果味和桃子味的糖？（同时手中有圆形苹果味匹配五角星桃子味糖果，或者有圆形桃子味匹配五角星苹果味糖果都满足要求）

苹果味  桃子味  西瓜味
圆形       7      9      8
五角星形   7      6      4

先给出整数答案，再证明该数量足够且少取一颗不够。`

// Kept as a named alias for callers that need to identify the pinned CPA
// wording. It must never diverge from OpenAIEvalCandyPrompt.
var OpenAIEvalCandyCPAReferencePrompt = OpenAIEvalCandyPrompt

// OpenAIEvalSupportedModels lists text models from the versioned local OpenAI
// catalog. Account-specific model discovery is intersected at run time.
func OpenAIEvalSupportedModels() []openai.Model {
	models := make([]openai.Model, 0, len(openai.DefaultModels))
	for _, model := range openai.DefaultModels {
		if strings.HasPrefix(strings.ToLower(model.ID), "gpt-") && !isOpenAIImageModel(model.ID) {
			models = append(models, model)
		}
	}
	return models
}

func isOpenAIEvalSupportedModel(modelID string) bool {
	modelID = strings.TrimSpace(modelID)
	for _, model := range OpenAIEvalSupportedModels() {
		if strings.EqualFold(model.ID, modelID) {
			return true
		}
	}
	return false
}

func OpenAIEvalFingerprintSampleCount(mode string) (int, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "quick":
		return OpenAIEvalFingerprintQuickSamples, nil
	case "standard":
		return OpenAIEvalFingerprintStandardSamples, nil
	case "strict":
		return OpenAIEvalFingerprintStrictSamples, nil
	default:
		return 0, fmt.Errorf("unsupported fingerprint mode %q", mode)
	}
}

func OpenAIEvalFingerprintPlan(mode string) (cells, repeats int, err error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "quick":
		return openAIEvalFingerprintQuickCells, openAIEvalFingerprintQuickRepeats, nil
	case "standard":
		return openAIEvalFingerprintStandardCells, openAIEvalFingerprintStandardRepeats, nil
	case "strict":
		return openAIEvalFingerprintStrictCells, openAIEvalFingerprintStrictRepeats, nil
	default:
		return 0, 0, fmt.Errorf("unsupported fingerprint mode %q", mode)
	}
}

func OpenAIEvalFingerprintCostEstimate(samples int, inputTokensPerRequest int, inputPricePerMillion float64) float64 {
	if samples <= 0 || inputTokensPerRequest <= 0 || inputPricePerMillion <= 0 {
		return 0
	}
	return float64(samples*inputTokensPerRequest) * inputPricePerMillion / 1_000_000
}

func ScoreOpenAIEvalCandy(answer string) OpenAIEvalOutcome {
	if value, ok := firstEvalNumber(answer); ok && value == OpenAIEvalCandyExpectedAnswer {
		return OpenAIEvalOutcome{Status: "pass", Reason: "correct_answer", Score: 1, SampleCount: 1, ExpectedCount: 1, Confidence: "low", Scheduling: "neutral"}
	}
	return OpenAIEvalOutcome{Status: "warning", Reason: "single_public_item_failed", Score: 0, SampleCount: 1, ExpectedCount: 1, Confidence: "low", Scheduling: "alert_only"}
}

func firstEvalNumber(value string) (int, bool) {
	runes := []rune(value)
	for i := 0; i < len(runes); i++ {
		if isEvalDigit(runes[i]) {
			start := i
			for i < len(runes) && isEvalDigit(runes[i]) {
				i++
			}
			decimal := start > 0 && runes[start-1] == '.' || i < len(runes) && runes[i] == '.'
			if parsed, ok := parseEvalNumber(string(runes[start:i])); ok && !decimal {
				return parsed, true
			}
			i--
		} else if isChineseEvalNumeral(runes[i]) {
			start := i
			for i < len(runes) && isChineseEvalNumeral(runes[i]) {
				i++
			}
			if parsed, ok := parseEvalNumber(string(runes[start:i])); ok {
				return parsed, true
			}
			i--
		}
	}
	return 0, false
}

func isEvalDigit(r rune) bool {
	return r >= '0' && r <= '9' || r >= '０' && r <= '９'
}

func isChineseEvalNumeral(r rune) bool {
	return strings.ContainsRune("零〇一二两三四五六七八九十百千", r)
}

func NormalizeOpenAIEvalFingerprintAnswer(raw string, probe OpenAIEvalProbe) (string, bool) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" || containsEvalRefusal(raw) {
		return "", false
	}
	word := firstEvalWord(raw)
	if word == "" {
		return "", false
	}
	switch probe.Kind {
	case "int":
		n, ok := parseEvalNumber(word)
		if !ok || n < probe.Low || n > probe.High {
			return "", false
		}
		return fmt.Sprint(n), true
	case "coin":
		switch {
		case word == "heads" || word == "head" || word == "字" || strings.HasPrefix(word, "正"):
			return "heads", true
		case word == "tails" || word == "tail" || word == "花" || strings.HasPrefix(word, "反"):
			return "tails", true
		default:
			return "", false
		}
	case "letter":
		if len(word) == 1 && word[0] >= 'a' && word[0] <= 'z' {
			return word, true
		}
		return "", false
	default:
		if len([]rune(word)) > 40 || strings.ContainsAny(word, "0123456789") {
			return "", false
		}
		return word, true
	}
}

func OpenAIEvalFingerprintJSD(a, b []string) float64 {
	if len(a) == 0 || len(b) == 0 {
		return math.NaN()
	}
	counts := map[string][2]float64{}
	for _, value := range a {
		count := counts[value]
		count[0]++
		counts[value] = count
	}
	for _, value := range b {
		count := counts[value]
		count[1]++
		counts[value] = count
	}
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	distance := 0.0
	for _, key := range keys {
		count := counts[key]
		p, q := count[0]/float64(len(a)), count[1]/float64(len(b))
		m := (p + q) / 2
		if p > 0 {
			distance += p * math.Log2(p/m) / 2
		}
		if q > 0 {
			distance += q * math.Log2(q/m) / 2
		}
	}
	if distance < 0 {
		return 0
	}
	if distance > 1 {
		return 1
	}
	return distance
}

func firstEvalWord(value string) string {
	runes := []rune(strings.TrimSpace(value))
	start := 0
	for start < len(runes) && !unicode.IsLetter(runes[start]) && !unicode.IsDigit(runes[start]) {
		start++
	}
	if start == len(runes) {
		return ""
	}
	isNumeric := isEvalDigit(runes[start]) || isChineseEvalNumeral(runes[start])
	end := start
	for end < len(runes) {
		if isNumeric {
			if !isEvalDigit(runes[end]) && !isChineseEvalNumeral(runes[end]) {
				break
			}
		} else if !unicode.IsLetter(runes[end]) && !unicode.IsDigit(runes[end]) {
			if runes[end] != '-' || end+1 >= len(runes) || !unicode.IsLetter(runes[end+1]) {
				break
			}
		}
		end++
	}
	return string(runes[start:end])
}

func parseEvalNumber(value string) (int, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}

	var ascii strings.Builder
	allDigits := true
	for _, r := range value {
		switch {
		case r >= '0' && r <= '9':
			ascii.WriteRune(r)
		case r >= '０' && r <= '９':
			ascii.WriteRune('0' + (r - '０'))
		default:
			allDigits = false
		}
	}
	if allDigits {
		n, err := strconv.Atoi(ascii.String())
		return n, err == nil
	}

	if n, ok := parseEnglishEvalNumber(value); ok {
		return n, true
	}

	return parseChineseEvalNumber(value)
}

// parseEnglishEvalNumber accepts the compact number words commonly returned
// by English fingerprint probes (for example "seventy" or "forty-two").
// It deliberately handles only cardinal numbers through 999 so explanatory
// prose cannot be mistaken for a probe answer.
func parseEnglishEvalNumber(value string) (int, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", " ")
	parts := strings.Fields(value)
	if len(parts) == 0 || len(parts) > 3 {
		return 0, false
	}
	ones := map[string]int{"zero": 0, "one": 1, "two": 2, "three": 3, "four": 4, "five": 5, "six": 6, "seven": 7, "eight": 8, "nine": 9, "ten": 10, "eleven": 11, "twelve": 12, "thirteen": 13, "fourteen": 14, "fifteen": 15, "sixteen": 16, "seventeen": 17, "eighteen": 18, "nineteen": 19}
	tens := map[string]int{"twenty": 20, "thirty": 30, "forty": 40, "fifty": 50, "sixty": 60, "seventy": 70, "eighty": 80, "ninety": 90}
	if len(parts) == 1 {
		if n, ok := ones[parts[0]]; ok {
			return n, true
		}
		if n, ok := tens[parts[0]]; ok {
			return n, true
		}
		return 0, false
	}
	if len(parts) == 2 {
		if tensValue, ok := tens[parts[0]]; ok {
			if onesValue, ok := ones[parts[1]]; ok && onesValue > 0 && onesValue < 10 {
				return tensValue + onesValue, true
			}
		}
		return 0, false
	}
	if parts[1] != "hundred" {
		return 0, false
	}
	hundreds, ok := ones[parts[0]]
	if !ok || hundreds == 0 {
		return 0, false
	}
	if parts[2] == "" {
		return hundreds * 100, true
	}
	if n, ok := ones[parts[2]]; ok {
		return hundreds*100 + n, true
	}
	if n, ok := tens[parts[2]]; ok {
		return hundreds*100 + n, true
	}
	return 0, false
}

func parseChineseEvalNumber(value string) (int, bool) {
	digits := map[rune]int{'零': 0, '〇': 0, '一': 1, '二': 2, '两': 2, '三': 3, '四': 4, '五': 5, '六': 6, '七': 7, '八': 8, '九': 9}
	units := map[rune]int{'十': 10, '百': 100, '千': 1000}
	total, section, digit := 0, 0, 0
	usedUnit := false
	for _, r := range value {
		if n, ok := digits[r]; ok {
			digit = n
			continue
		}
		unit, ok := units[r]
		if !ok {
			return 0, false
		}
		usedUnit = true
		if digit == 0 && unit == 10 {
			digit = 1
		} else if digit == 0 {
			return 0, false
		}
		section += digit * unit
		digit = 0
	}
	if !usedUnit {
		return 0, false
	}
	return total + section + digit, true
}

func containsEvalRefusal(value string) bool {
	for _, phrase := range []string{
		"i cannot", "i can't", "i am unable", "i'm unable", "unable to", "sorry,", "as an ai",
		"抱歉", "无法", "不能回答", "不能提供", "作为一个人工智能",
	} {
		if strings.Contains(value, phrase) {
			return true
		}
	}
	return false
}

func ScoreOpenAIEvalFingerprint(model string, samples []OpenAIEvalSample, baselines []OpenAIEvalFingerprintBaseline, required int) OpenAIEvalFingerprintResult {
	result := OpenAIEvalFingerprintResult{Status: "insufficient", ValidSamples: 0, RequiredSamples: required, EvaluatedAt: time.Now().UTC()}
	valid := map[string][]string{}
	for _, sample := range samples {
		if sample.Error != "" {
			continue
		}
		probe, ok := findOpenAIEvalProbe(sample.ProbeID)
		if !ok {
			continue
		}
		answer, ok := NormalizeOpenAIEvalFingerprintAnswer(sample.Answer, probe)
		if !ok {
			continue
		}
		valid[probe.ID] = append(valid[probe.ID], answer)
		result.ValidSamples++
	}
	if result.ValidSamples < required || len(baselines) == 0 {
		result.Reason = "insufficient_valid_samples"
		if len(baselines) == 0 {
			result.Reason = "no_versioned_baseline"
		}
		return result
	}
	baselineCount := len(baselines)
	alpha := .05 / float64(baselineCount)
	for _, baseline := range baselines {
		cellScores := make([]float64, 0, len(valid))
		for _, probe := range OpenAIEvalFingerprintProbes {
			cell := probe.ID
			candidate := valid[cell]
			reference := baseline.Cells[cell]
			if len(candidate) < 10 || len(reference) < 10 {
				continue
			}
			cellScores = append(cellScores, OpenAIEvalFingerprintJSD(candidate, reference))
		}
		if len(cellScores) < 4 {
			continue
		}
		mean := 0.0
		for _, score := range cellScores {
			mean += score
		}
		mean /= float64(len(cellScores))
		if result.MeanJSD == nil || mean < *result.MeanJSD {
			result.MeanJSD = &mean
			result.NearestModel = baseline.Model
			result.CellCount = len(cellScores)
			p := openAIEvalFingerprintPermutationPValue(valid, baseline.Cells, model+"|"+baseline.Model, openAIEvalFingerprintPermutationN)
			result.PValue = p
		}
	}
	if result.MeanJSD == nil {
		result.Status, result.Reason = "insufficient", "insufficient_cells"
		return result
	}
	significant := result.PValue != nil && *result.PValue < alpha
	if strings.EqualFold(result.NearestModel, model) && !significant {
		result.Status, result.Reason = "consistent", "behavior_distribution_consistent_with_versioned_reference"
	} else if significant {
		result.Status, result.Reason = "different", "behavior_distribution_differs_from_reference"
	} else {
		result.Status, result.Reason = "uncertain", "fingerprint_is_identity_evidence_not_capability"
	}
	return result
}

func openAIEvalFingerprintPermutationPValue(a, b map[string][]string, seed string, permutations int) *float64 {
	entries := make([]string, 0)
	for _, probe := range OpenAIEvalFingerprintProbes {
		if len(a[probe.ID]) >= 10 && len(b[probe.ID]) >= 10 {
			entries = append(entries, probe.ID)
		}
	}
	if len(entries) < 4 || permutations <= 0 {
		return nil
	}
	observed := 0.0
	pools := make([][]string, len(entries))
	sizes := make([]int, len(entries))
	for i, cell := range entries {
		observed += OpenAIEvalFingerprintJSD(a[cell], b[cell])
		pools[i] = append(append([]string(nil), a[cell]...), b[cell]...)
		sizes[i] = len(a[cell])
	}
	observed /= float64(len(entries))
	rng := rand.New(rand.NewSource(int64(crc32.ChecksumIEEE([]byte(seed)))))
	hits := 0
	for range permutations {
		total := 0.0
		for i, pool := range pools {
			shuffled := append([]string(nil), pool...)
			rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
			total += OpenAIEvalFingerprintJSD(shuffled[:sizes[i]], shuffled[sizes[i]:])
		}
		if total/float64(len(entries)) >= observed-1e-12 {
			hits++
		}
	}
	p := float64(hits+1) / float64(permutations+1)
	return &p
}

func findOpenAIEvalProbe(id string) (OpenAIEvalProbe, bool) {
	for _, probe := range OpenAIEvalFingerprintProbes {
		if probe.ID == id {
			return probe, true
		}
	}
	return OpenAIEvalProbe{}, false
}

func OpenAIEvalBaselineVersionFor(models []string) string {
	copyModels := append([]string(nil), models...)
	sort.Strings(copyModels)
	hash := sha256.Sum256([]byte(strings.Join(copyModels, "\n")))
	return OpenAIEvalBaselineVersion + "-" + hex.EncodeToString(hash[:4])
}

func OpenAIEvalSchedulingDisposition(testType string, outcome OpenAIEvalOutcome, globalEnabled bool) string {
	if !globalEnabled {
		return "disabled"
	}
	if strings.EqualFold(testType, OpenAIEvalTypeFingerprint) || strings.EqualFold(testType, OpenAIEvalTypeCandy) || strings.EqualFold(testType, OpenAIEvalTypeModelTrace) {
		return "alert_only"
	}
	if outcome.Status == "fail" && outcome.Confidence == "high" {
		return "downrank_route"
	}
	return "alert_only"
}

// OpenAIEvalRoutePenalty returns the only evaluation-derived scheduler signal
// currently allowed: an explicit, high-confidence operational failure. Candy
// and Fingerprint outcomes are intentionally excluded because they are low
// confidence and/or identity evidence, not a capability verdict.
func OpenAIEvalRoutePenalty(testType string, outcome OpenAIEvalOutcome, globalEnabled bool) float64 {
	if !globalEnabled || strings.EqualFold(testType, OpenAIEvalTypeCandy) || strings.EqualFold(testType, OpenAIEvalTypeFingerprint) || strings.EqualFold(testType, OpenAIEvalTypeModelTrace) {
		return 0
	}
	if outcome.Status == "fail" && strings.EqualFold(outcome.Confidence, "high") && outcome.SampleCount >= outcome.ExpectedCount && outcome.ExpectedCount > 0 {
		return 1
	}
	return 0
}
