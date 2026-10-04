//go:build unit

package service

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

const candy21UserFormattedAnswer = `**最少取出 \(21\) 个**（利用题目所给条件：可以凭手感辨别形状，并选择取出哪种形状）。

### 取法：9 个圆形＋12 个五角星形

- **取 9 个圆形糖果**：圆形西瓜味只有 8 个，因此其中至少有一个苹果味或桃子味。
- **取 12 个五角星形糖果**：
  - 非苹果味的五角星形只有 \(6+4=10\) 个，所以必有苹果味；
  - 非桃子味的五角星形只有 \(7+4=11\) 个，所以必有桃子味。

因此，无论圆形糖果中出现的是苹果味还是桃子味，都能与五角星形中另一种口味配成符合要求的一对。

### 为什么 20 个不能保证？

考虑最不利的摸取次序：
- 圆形：先摸到 8 个西瓜味，再摸到 7 个苹果味，最后才摸到桃子味；
- 五角星形：先摸到 4 个西瓜味，再摸到 7 个苹果味，最后才摸到桃子味。

取出 20 个，设其中有 \(r\) 个圆形：
- 若 \(r\le8\)，圆形全是西瓜味，无法配对；
- 若 \(9\le r\le15\)，五角星形最多 11 个，两种形状中都没有桃子味，无法配对；
- 若 \(r\ge16\)，五角星形最多 4 个，全是西瓜味，仍无法配对。

所以 20 个不能保证，答案是 **\(\boxed{21}\) 个**。

*注：若不允许凭手感选择形状，只能完全随机取出，则答案是 29 个；28 个可能恰好是全部圆形苹果、圆形桃子和全部西瓜味糖果，仍不符合要求。*`

func TestOpenAIEvalCandyExpectedNumberPresence(t *testing.T) {
	for _, answer := range []string{
		candy21UserFormattedAnswer,
		`最少取出 \(21\) 个`,
		`答案为 $\boxed{21}$。`,
		`正文包含 21，但最后写了29。`,
		`可能是21，也可能不是。`,
		`21不够，最终答案29。`,
		`圆形21个，星形12个。`,
		`出现全角数字２１。`,
	} {
		t.Run(answer, func(t *testing.T) {
			value, ok := leadingOpenAIEvalCandyAnswer(answer)
			require.True(t, ok)
			require.Equal(t, 21, value)
			require.Equal(t, "pass", ScoreOpenAIEvalCandy(answer).Status)
		})
	}
	for _, answer := range []string{"121", "21.5", "0.21", ".21", "２１．５", "21e3", "答案：29。", "无法确定。"} {
		t.Run(answer, func(t *testing.T) {
			require.Equal(t, "warning", ScoreOpenAIEvalCandy(answer).Status)
		})
	}
}

func TestEvalCandyFormattedPresencePersistsExtractedAnswer(t *testing.T) {
	svc, repo, _ := evalRunHarness(t, func(*http.Request, int) (*http.Response, error) {
		return newJSONResponse(200, evalCompletedJSON(candy21UserFormattedAnswer)), nil
	})
	run, err := svc.Run(t.Context(), OpenAIEvalRunRequest{AccountID: 995, TestType: OpenAIEvalTypeCandy, RequestedModel: "gpt-5.4"}, 1, "manual")
	require.NoError(t, err)
	require.Equal(t, "pass", run.Status)
	require.Len(t, run.Samples, 1)
	require.Equal(t, "21", run.Samples[0].NormalizedAnswer)
	require.Equal(t, "correct_answer", run.Samples[0].ErrorCode)
	require.Equal(t, candy21UserFormattedAnswer, run.Samples[0].Answer)
	require.Equal(t, "21", repo.runs[0].Samples[0].NormalizedAnswer)
}
