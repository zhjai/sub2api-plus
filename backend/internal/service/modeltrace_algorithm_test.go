package service

import (
	"fmt"
	"strings"
	"testing"
)

func TestTraceParseNumbersIgnoresUnrelatedText(t *testing.T) {
	values := traceParseNumbers("结果: 1, 2, 3; 355。")
	if len(values) != 4 || values[0] != 1 || values[3] != 355 {
		t.Fatalf("unexpected parsed values: %#v", values)
	}
}

func TestAnalyzeModelTraceRejectsInsufficientOutputs(t *testing.T) {
	if _, err := analyzeModelTrace([]traceOutput{{Text: "1 2 3", ExpectedCount: 300}}); err == nil {
		t.Fatal("expected insufficient ModelTrace output error")
	}
}

func TestAnalyzeModelTraceProducesAttribution(t *testing.T) {
	makeOutput := func(offset int) string {
		values := make([]string, 300)
		for i := range values {
			values[i] = fmt.Sprintf("%d", (i+offset)%355+1)
		}
		return strings.Join(values, " ")
	}
	outputs := []traceOutput{{Text: makeOutput(0), ExpectedCount: 300}, {Text: makeOutput(17), ExpectedCount: 300}, {Text: makeOutput(31), ExpectedCount: 300}}
	result, err := analyzeModelTrace(outputs)
	if err != nil {
		t.Fatalf("analyzeModelTrace: %v", err)
	}
	if result.Used != 3 || result.Prediction == "" || len(result.Results) == 0 {
		t.Fatalf("unexpected attribution: %#v", result)
	}
}
