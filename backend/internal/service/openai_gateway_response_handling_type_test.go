package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIStreamEventIsTerminalWithTypeMatchesExistingSemantics(t *testing.T) {
	tests := []struct {
		name string
		data string
		want bool
	}{
		{name: "empty", data: "", want: false},
		{name: "whitespace", data: " \t ", want: false},
		{name: "done", data: " [DONE] ", want: true},
		{name: "JSON outer whitespace", data: " \n\t {\"type\":\"response.completed\"} \r\n", want: true},
		{name: "completed", data: `{"type":"response.completed"}`, want: true},
		{name: "response done", data: `{"type":"response.done"}`, want: true},
		{name: "failed", data: `{"type":"response.failed"}`, want: true},
		{name: "incomplete", data: `{"type":"response.incomplete"}`, want: true},
		{name: "cancelled", data: `{"type":"response.cancelled"}`, want: true},
		{name: "canceled", data: `{"type":"response.canceled"}`, want: true},
		{name: "delta", data: `{"type":"response.output_text.delta"}`, want: false},
		{name: "invalid JSON", data: `{"type":`, want: false},
		{name: "terminal with trailing garbage", data: `{"type":"response.completed"} trailing`, want: true},
		{name: "nonterminal with trailing garbage", data: `{"type":"response.output_text.delta"} trailing`, want: false},
		{name: "type whitespace is normalized", data: `{"type":" response.completed "}`, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eventType := gjson.GetBytes([]byte(tt.data), "type").String()
			got := openAIStreamEventIsTerminalWithType(tt.data, eventType)

			require.Equal(t, tt.want, got)
			require.Equal(t, openAIStreamEventIsTerminal(tt.data), got)
		})
	}
}

func TestClassifyOpenAIResponsesOutcome(t *testing.T) {
	tests := []struct {
		name           string
		eventType      string
		payload        string
		protocolStatus string
		responseStatus string
		reason         string
	}{
		{name: "completed", eventType: "response.completed", payload: `{"type":"response.completed","response":{"status":"completed"}}`, protocolStatus: "completed", responseStatus: "completed"},
		{name: "done incomplete", eventType: "response.done", payload: `{"type":"response.done","response":{"status":"incomplete","incomplete_details":{"reason":"stream_terminated"}}}`, protocolStatus: "incomplete", responseStatus: "incomplete", reason: "stream_terminated"},
		{name: "done failed", eventType: "response.done", payload: `{"type":"response.done","status":"failed"}`, protocolStatus: "failed", responseStatus: "failed"},
		{name: "cancelled", eventType: "response.canceled", payload: `{"type":"response.canceled","response":{"status":"canceled"}}`, protocolStatus: "cancelled", responseStatus: "canceled"},
		{name: "explicit incomplete", eventType: "response.incomplete", payload: `{"type":"response.incomplete","response":{"incomplete_details":{"reason":"max_output_tokens"}}}`, protocolStatus: "incomplete", reason: "max_output_tokens"},
		{name: "done marker", eventType: "[DONE]", payload: "[DONE]", protocolStatus: "completed"},
		{name: "status fallback", payload: `{"status":"incomplete","incomplete_details":{"reason":"stream_terminated"}}`, protocolStatus: "incomplete", responseStatus: "incomplete", reason: "stream_terminated"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, responseStatus, reason := classifyOpenAIResponsesOutcome(tt.eventType, []byte(tt.payload))
			require.Equal(t, tt.protocolStatus, status)
			require.Equal(t, tt.responseStatus, responseStatus)
			require.Equal(t, tt.reason, reason)
		})
	}
}

var (
	benchmarkOpenAIResponseSSEEventTypeSink string
	benchmarkOpenAIResponseSSETerminalSink  bool
)

func BenchmarkOpenAIResponseSSETypeExtraction(b *testing.B) {
	data := `{"type":"response.output_text.delta","sequence_number":42,"delta":"streaming response benchmark payload"}`
	dataBytes := []byte(data)

	b.Run("legacy double parse", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(dataBytes)))
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			benchmarkOpenAIResponseSSETerminalSink = openAIStreamEventIsTerminal(data)
			benchmarkOpenAIResponseSSEEventTypeSink = strings.TrimSpace(gjson.GetBytes(dataBytes, "type").String())
		}
	})

	b.Run("reused single parse", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(dataBytes)))
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			eventTypeRaw := gjson.GetBytes(dataBytes, "type").String()
			benchmarkOpenAIResponseSSEEventTypeSink = strings.TrimSpace(eventTypeRaw)
			benchmarkOpenAIResponseSSETerminalSink = openAIStreamEventIsTerminalWithType(data, eventTypeRaw)
		}
	})
}
