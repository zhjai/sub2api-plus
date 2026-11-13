package basispoints

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNamespaceDescriptionsSurviveCatalogReplayAndReprepare(t *testing.T) {
	namespace := object{"type": "namespace", "name": "outer", "description": "Outer scope rules.", "tools": []any{
		object{"type": "namespace", "name": "inner", "description": "Inner scope rules.", "tools": []any{catalogReadTool("string")}},
	}}
	for _, discovery := range []bool{false, true} {
		cache := new(CatalogCache)
		source := testSource()
		if discovery {
			source["input"] = []any{object{"type": "tool_search_output", "tools": []any{namespace}}, message("user", "continue")}
		} else {
			source["tools"] = []any{namespace}
		}
		raw, err := json.Marshal(source)
		require.NoError(t, err)
		body, _, err := PrepareWithCatalog(raw, "scope", nil, cache)
		require.NoError(t, err)
		require.Contains(t, string(body), "Outer scope rules.")
		require.Contains(t, string(body), "Inner scope rules.")
		delete(source, "tools")
		source["input"] = "next turn"
		raw, err = json.Marshal(source)
		require.NoError(t, err)
		body, bridge, err := PrepareWithCatalog(raw, "scope", nil, cache)
		require.NoError(t, err)
		for _, phrase := range []string{"outer.inner.read_file", "Outer scope rules.", "Inner scope rules."} {
			require.Contains(t, string(body), phrase)
		}
		body, _, err = bridge.Reprepare(raw)
		require.NoError(t, err)
		require.Contains(t, string(body), "Outer scope rules.")
		require.Contains(t, string(body), "Inner scope rules.")
	}
}

func TestReadableReasoningHistoryPreservesTextAndCiphertext(t *testing.T) {
	for _, ciphertext := range []string{"", "opaque-ciphertext"} {
		source := testSource()
		const summary = "Exact summary.\nKeep \"quotes\" and Unicode 中文."
		source["input"] = []any{object{"type": "reasoning", "encrypted_content": ciphertext,
			"summary": []any{object{"type": "summary_text", "text": summary}}}, message("user", "continue")}
		body, _ := mustPrepare(t, source, "scope", nil)
		items := mustTestValue[[]any](t, body["input"])
		foundText, foundCiphertext := false, false
		for _, raw := range items {
			item := mustTestValue[object](t, raw)
			if item["type"] == "reasoning" {
				require.Equal(t, ciphertext, item["encrypted_content"])
				foundCiphertext = true
			}
			if item["role"] == "assistant" {
				parts := mustTestValue[[]any](t, item["content"])
				require.Contains(t, parts[0].(object)["text"], "Historical assistant reasoning summary")
				require.Equal(t, summary, parts[1].(object)["text"])
				foundText = true
			}
		}
		require.True(t, foundText)
		require.Equal(t, ciphertext != "", foundCiphertext)
	}
}

func TestReasoningSummaryUnknownPartsFailWithoutPayloadLeak(t *testing.T) {
	source := testSource()
	source["input"] = []any{object{"type": "reasoning", "summary": []any{object{"type": "unknown", "text": "PRIVATE_SUMMARY"}}}}
	raw, err := json.Marshal(source)
	require.NoError(t, err)
	_, _, err = Prepare(raw, "scope", nil)
	require.ErrorContains(t, err, "input[0].summary[0]")
	require.NotContains(t, err.Error(), "PRIVATE_SUMMARY")
}

func TestParallelToolFlagMatchesPolicyAcrossLifecycle(t *testing.T) {
	for _, policy := range []any{nil, false, true} {
		source := testSource()
		source["tools"] = []any{object{"type": "function", "name": "get_weather", "parameters": object{"type": "object"}}}
		if policy != nil {
			source["parallel_tool_calls"] = policy
		}
		body, bridge := mustPrepare(t, source, "scope", nil)
		enabled := policy != false
		encoded, err := json.Marshal(body)
		require.NoError(t, err)
		require.Equal(t, enabled, strings.Contains(string(encoded), "Independent client tools may be called in parallel"))
		for _, kind := range []string{"response.created", "response.in_progress", "response.incomplete", "response.failed", "response.completed"} {
			wire := sse(object{"type": kind, "response": object{"id": "resp_policy", "output": []any{}}})
			if kind == "response.created" || kind == "response.in_progress" {
				wire += structuredTerminal("response.completed")
			}
			events := structuredEvents(t, bridge, wire)
			response := mustTestValue[object](t, events[0]["response"])
			require.Equal(t, enabled, response["parallel_tool_calls"], kind)
		}
		first := nativeCall(object{"name": "get_weather", "arguments": object{}})
		second := nativeCall(object{"name": "get_weather", "arguments": object{}})
		second["call_id"], second["id"] = "call_second", "fc_second"
		err = bridge.translateResponse(object{"output": []any{first, second}})
		if enabled {
			require.NoError(t, err)
		} else {
			require.ErrorContains(t, err, "tool call count limit")
		}
	}
}

func TestCurrentParentAssignmentVersusHistoricalAgentContext(t *testing.T) {
	for _, tc := range []struct {
		author, recipient, tail string
		current                 bool
	}{
		{"/root", "/root/worker", "", true},
		{"/root", "/root/worker", "additional_tools", true},
		{"/root", "/root/worker", "user", false},
		{"/root/worker", "/root", "", false},
	} {
		source := testSource()
		assignment := object{"type": "agent_message", "author": tc.author, "recipient": tc.recipient, "content": "Exact task text"}
		input := []any{assignment}
		if tc.tail == "user" {
			input = append(input, message("user", "New task"))
		} else if tc.tail != "" {
			input = append(input, object{"type": tc.tail, "tools": []any{}})
		}
		source["input"] = input
		body, _ := mustPrepare(t, source, "scope", nil)
		items := mustTestValue[[]any](t, body["input"])
		lowered := mustTestValue[object](t, items[2])
		require.Equal(t, "user", lowered["role"])
		parts := mustTestValue[[]any](t, lowered["content"])
		label := text(parts[0].(object)["text"])
		require.Equal(t, tc.current, strings.Contains(label, "Current task assigned by the parent agent"))
		if tc.current {
			require.Contains(t, label, "subject to system and developer instructions")
		}
		require.Equal(t, "Exact task text", parts[1].(object)["text"])
	}
}

func TestUnsupportedControlsAreVisibleWithoutForwardingOrValueLeak(t *testing.T) {
	source := testSource()
	source["max_output_tokens"] = 127
	source["max_tool_calls"] = 3
	source["include"] = []any{"PRIVATE_VALUE"}
	source["reasoning"] = object{"effort": "minimal", "summary": "auto"}
	source["text"] = object{"verbosity": "high"}
	body, bridge := mustPrepare(t, source, "scope", nil)
	warnings := strings.Join(bridge.Warnings, "\n")
	for _, field := range []string{"max_output_tokens", "max_tool_calls", "include", "reasoning.summary", "text.verbosity"} {
		require.Contains(t, warnings, field)
	}
	require.Contains(t, warnings, "none/minimal to low")
	require.NotContains(t, warnings, "PRIVATE_VALUE")
	for _, field := range []string{"max_output_tokens", "max_tool_calls", "include", "reasoning", "text"} {
		require.NotContains(t, body, field)
	}
	require.Equal(t, "low", body["reasoning_effort"])
}
