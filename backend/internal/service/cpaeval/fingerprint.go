package cpaeval

import (
	_ "embed"
	"errors"
	"math/rand"
	"strconv"
	"strings"
)

// Each run sends fingerprintTarget challenges, at most that many at once per credential and
// fingerprintGlobalConcurrency across credentials. A challenge holds its slot through its retries.
const (
	fingerprintTarget            = 3
	fingerprintGlobalConcurrency = 6
	fingerprintMaxText           = 64000
)

type fingerprintChallenge struct {
	Instructions  string `json:"instructions,omitempty"`
	Prompt        string `json:"prompt"`
	ExpectedCount int    `json:"expected_count"`
}

//go:embed data/lm-detector/enrollment-suite.json
var fingerprintSuiteJSON []byte

// fingerprintSuite holds the challenges the reference answers were collected with: 12 prompt environments that
// add a system prompt, a user prefix or nothing to their three challenges.
var fingerprintSuite = func() []fingerprintChallenge {
	var suite []fingerprintChallenge
	for _, c := range mustDecode[[]struct {
		System        string `json:"system"`
		UserPrefix    string `json:"user_prefix"`
		Prompt        string `json:"prompt"`
		ExpectedCount int    `json:"expected_count"`
	}](fingerprintSuiteJSON) {
		prompt := c.Prompt
		if c.UserPrefix != "" {
			prompt = c.UserPrefix + "\n\nFinal task:\n" + c.Prompt
		}
		suite = append(suite, fingerprintChallenge{c.System, prompt, c.ExpectedCount})
	}
	return suite
}()

func fingerprintChallenges() []fingerprintChallenge {
	challenges := make([]fingerprintChallenge, fingerprintTarget)
	for i, j := range rand.Perm(len(fingerprintSuite))[:fingerprintTarget] {
		challenges[i] = fingerprintSuite[j]
	}
	return challenges
}

// fingerprintAliases maps tested models to the candidate that stands for them: the bank enrolls gpt-6.1-sol
// answers under gpt-6-astra, and deepseek-flash serves the model enrolled as deepseek-v4.1-flash-1017.
// The page compares models with the same table.
var fingerprintAliases = map[string]string{"gpt-6.1-sol": "gpt-6-astra", "deepseek-flash": "deepseek-v4.1-flash-1017"}

// fingerprintModelID is the candidate a model name stands for, without a provider prefix such as "openai/".
func fingerprintModelID(model string) string {
	model = strings.ToLower(strings.TrimSpace(model))
	model = model[strings.LastIndex(model, "/")+1:]
	if alias, ok := fingerprintAliases[model]; ok {
		return alias
	}
	return model
}

// fingerprintText returns the text of answer that the detector scores and the count of its numbers, or why
// the answer cannot be scored. Like the upstream detector, only the requested count is scored when an answer runs
// longer.
func fingerprintText(answer string, expected int) (text string, parsed int, err error) {
	if len(answer) > fingerprintMaxText {
		return "", 0, errors.New("回答过长")
	}
	text, numbers := answer, parseNumbers(answer)
	if len(numbers) > expected {
		numbers = numbers[:expected]
		values := make([]string, len(numbers))
		for i, n := range numbers {
			values[i] = strconv.Itoa(n)
		}
		text = strings.Join(values, ", ")
	}
	if len(numbers) < minimumNumbers(expected) {
		err = errors.New("有效数字不足")
	}
	return text, len(numbers), err
}
