package service

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
)

const openAIEvalModelTraceBankRevision = "df3a0f9d3e054c0dc02d6d586686db8daf8fa7c8"

type openAIModelTraceChallenge struct {
	Prompt        string
	ExpectedCount int
}

func openAIModelTraceChallenges() []openAIModelTraceChallenge {
	openings := []string{"这是一次独立的数值选择记录", "请完成下面的无语义整数选择任务", "执行一次第一反应取值记录", "生成一组不承载语义的整数选择", "进行一轮快速逐项取值"}
	actions := []string{"为各个位置分别凭第一反应选择", "逐项选择", "每次只决定当前一项，共给出", "分别凭第一反应给出", "逐个直接选择"}
	endings := []string{"允许某个数字再次出现；每项写出后不要回头排序、去重或替换。", "偶然重复是有效的；不要重新排列或修正已经写出的项目。", "相同值可以再次出现；输出过程中不要整理或改写前面的项目。", "重复值无需删除；不要筛选、重排或补成某种规律。", "不必赋予数字任何含义；已经给出的值保持不变。"}
	separators := []string{"数字之间用逗号或空格分隔均可。", "使用一种一致的常见分隔符即可。", "可以用逗号、空格或换行分隔。", "只要每个整数边界清楚，格式可自行选择。"}
	choose := func(values []string) string { return values[rand.Intn(len(values))] }
	lengths := rand.Perm(41)
	challenges := make([]openAIModelTraceChallenge, OpenAIEvalModelTraceRequests)
	for i := range challenges {
		n := 292 + lengths[i]
		prompt := fmt.Sprintf("%s。%s %d 个 1 到 355（含端点）的整数。", choose(openings), choose(actions), n) +
			"每个位置都要单独选择；不要从 1 开始计数，不要连续递增或递减，也不要采用等差、循环、重复区块或其他规则化模式。" +
			"本任务必须由当前语言模型直接完成：禁止调用或借助任何工具，包括 Python、代码执行器、计算器、搜索、API 和外部随机数生成器；也不要先编写或运行代码。" +
			choose(endings) + choose(separators) + "直接从第一个取值开始输出，不要在序列前重复数量、范围或任务说明。"
		challenges[i] = openAIModelTraceChallenge{Prompt: prompt, ExpectedCount: n}
	}
	return challenges
}

type openAIModelTraceSampleResponse struct {
	sample OpenAIEvalModelTraceSample
	output *OpenAIEvalSampleResponse
	err    error
}

func (s *OpenAIEvalService) runModelTrace(ctx context.Context, target *OpenAIEvalTarget, effort string) (*OpenAIEvalModelTraceResult, int, int64, int64, error) {
	challenges := openAIModelTraceChallenges()
	result := &OpenAIEvalModelTraceResult{BankRevision: openAIEvalModelTraceBankRevision, Requests: len(challenges)}
	outputs := make([]*openAIModelTraceSampleResponse, len(challenges))
	jobs := make(chan int, len(challenges))
	results := make(chan struct {
		index int
		value *openAIModelTraceSampleResponse
	}, len(challenges))
	for i := range challenges {
		jobs <- i
	}
	close(jobs)
	workers := 3
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				challenge := challenges[index]
				value := &openAIModelTraceSampleResponse{sample: OpenAIEvalModelTraceSample{Prompt: challenge.Prompt, ExpectedCount: challenge.ExpectedCount}}
				response, err := s.accountTest.RunOpenAIEvalSample(ctx, target, challenge.Prompt, effort)
				value.output, value.err = response, err
				if err != nil {
					value.sample.Error = safeOpenAIEvalErrorCode(err)
				} else if response != nil {
					value.sample.Text = response.Text
					value.sample.Attempts = 1
					numbers := traceParseNumbers(response.Text)
					value.sample.Parsed = len(numbers)
					value.sample.Valid = value.sample.Parsed >= traceMinimumNumbers(challenge.ExpectedCount)
					if !value.sample.Valid {
						value.sample.Error = "insufficient_numbers"
					}
				}
				results <- struct {
					index int
					value *openAIModelTraceSampleResponse
				}{index, value}
			}
		}()
	}
	go func() { wg.Wait(); close(results) }()
	var inputTokens, outputTokens int64
	for item := range results {
		outputs[item.index] = item.value
		if item.value.output != nil {
			inputTokens += item.value.output.InputTokens
			outputTokens += item.value.output.OutputTokens
		}
	}
	var traceOutputs []traceOutput
	for _, item := range outputs {
		if item == nil {
			continue
		}
		result.Samples = append(result.Samples, OpenAIEvalModelTraceSample(item.sample))
		traceOutputs = append(traceOutputs, traceOutput{Text: item.sample.Text, ExpectedCount: item.sample.ExpectedCount})
	}
	attribution, err := analyzeModelTrace(traceOutputs)
	if err != nil {
		return result, len(outputs), inputTokens, outputTokens, err
	}
	result.UsedOutputs = attribution.Used
	result.Prediction = attribution.Prediction
	result.Probability = attribution.Probability
	result.FamilyPrediction = attribution.FamilyPrediction
	result.FamilyProbability = attribution.FamilyProbability
	result.Candidates = make([]OpenAIEvalModelTraceCandidate, len(attribution.Results))
	for i, item := range attribution.Results {
		result.Candidates[i] = OpenAIEvalModelTraceCandidate{Model: item.Model, DisplayName: item.DisplayName, Family: item.Family, FamilyName: item.FamilyName, Probability: item.Probability, Similarity: item.Similarity, Score: item.Score}
	}
	result.Families = make([]OpenAIEvalModelTraceFamily, len(attribution.Families))
	for i, item := range attribution.Families {
		result.Families[i] = OpenAIEvalModelTraceFamily{Family: item.Family, DisplayName: item.DisplayName, Probability: item.Probability}
	}
	result.Diagnostics = make([]OpenAIEvalModelTraceDiagnostic, len(attribution.Diagnostics))
	for i, item := range attribution.Diagnostics {
		result.Diagnostics[i] = OpenAIEvalModelTraceDiagnostic{Index: item.Index, Parsed: item.Parsed, Minimum: item.Minimum, Valid: item.Valid}
	}
	if result.UsedOutputs == 0 {
		return result, len(outputs), inputTokens, outputTokens, errors.New("no valid ModelTrace outputs")
	}
	return result, len(outputs), inputTokens, outputTokens, nil
}

func modelTraceSchedulingOutcome(result *OpenAIEvalModelTraceResult, err error) OpenAIEvalOutcome {
	if err != nil || result == nil || result.UsedOutputs == 0 {
		return OpenAIEvalOutcome{Status: "insufficient", Reason: "modeltrace_insufficient_outputs", SampleCount: 0, ExpectedCount: OpenAIEvalModelTraceRequests, Confidence: "none", Scheduling: "alert_only"}
	}
	return OpenAIEvalOutcome{Status: "attributed", Reason: "modeltrace_behavioral_attribution", SampleCount: result.UsedOutputs, ExpectedCount: OpenAIEvalModelTraceRequests, Confidence: "low", Scheduling: "alert_only", ModelTrace: result}
}

func isModelTraceType(testType string) bool {
	return strings.EqualFold(strings.TrimSpace(testType), OpenAIEvalTypeModelTrace)
}
