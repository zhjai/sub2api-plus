package basispoints

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCapabilityClassificationUsesActualContent(t *testing.T) {
	cases := []struct {
		name, body, code string
		historical       bool
	}{
		{"v2 and empty encryption", `{"multi_agent_version":"v2","input":[{"type":"function_call","encrypted_function_args":[],"arguments":"{}"}]}`, "", false},
		{"opaque native state", `{"input":[{"type":"reasoning","encrypted_content":"opaque"},{"type":"compaction","encrypted_content":"opaque"}]}`, "", false},
		{"genuine encrypted args", `{"input":[{"type":"function_call","encrypted_function_args":["message"],"arguments":"{}"}]}`, "basispoints_encrypted_arguments", false},
		{"current user ciphertext", `{"input":[{"role":"user","content":[{"type":"encrypted_content","encrypted_content":"opaque"}]}]}`, "basispoints_encrypted_context", false},
		{"old history", `{"input":[{"role":"assistant","content":[{"type":"encrypted_content","encrypted_content":"opaque"}]},{"role":"user","content":"next"}]}`, "basispoints_encrypted_context", true},
		{"active instructions", `{"input":[{"role":"developer","content":[{"type":"encrypted_content","encrypted_content":"opaque"}]},{"role":"user","content":"next"}]}`, "basispoints_encrypted_context", false},
		{"nested hosted", `{"tools":[{"type":"namespace","name":"ns","tools":[{"type":"web_search"}]}]}`, "basispoints_hosted_capability", false},
		{"disabled hosted declaration", `{"tools":[{"type":"web_search"}],"tool_choice":"none"}`, "", false},
		{"disabled hosted with history", `{"tools":[{"type":"web_search"}],"tool_choice":"none","input":[{"type":"web_search_call"}]}`, "basispoints_hosted_capability", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := InspectRequestCapabilities([]byte(tc.body))
			if tc.code == "" {
				require.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			require.Equal(t, tc.code, got.Code)
			require.Equal(t, tc.historical, got.Historical)
			require.NotContains(t, got.Error(), "opaque")
		})
	}
}

func TestHistoricalOmissionPreservesCurrentDependencies(t *testing.T) {
	raw := []byte(`{"instructions":"active instruction","input":[{"role":"assistant","content":[{"type":"encrypted_content","encrypted_content":"OLD"}]},{"role":"developer","content":[{"type":"encrypted_content","encrypted_content":"INSTRUCTION"}]},{"type":"compaction","encrypted_content":"STATE"},{"type":"function_call_output","call_id":"missing","output":[{"type":"encrypted_content","encrypted_content":"DEPENDENCY"}]},{"role":"user","content":[{"type":"encrypted_content","encrypted_content":"CURRENT"}]}]}`)
	out, err := StripEncryptedContent(raw)
	require.NoError(t, err)
	require.NotContains(t, string(out), `"OLD"`)
	for _, value := range []string{"INSTRUCTION", "STATE", "DEPENDENCY", "CURRENT", "active instruction"} {
		require.Contains(t, string(out), value)
	}
	require.NotNil(t, InspectRequestCapabilities(out))
}

func TestCompactRetainsCatalogForFollowingToolTurn(t *testing.T) {
	cache := new(CatalogCache)
	replay := new(ReplayCache)
	source := testSource()
	source["tools"] = []any{object{"type": "function", "name": "lookup", "parameters": object{"type": "object", "properties": object{}}}}
	source["input"] = []any{message("user", "retain exact history"), object{"type": "compaction_trigger"}}
	raw, _ := json.Marshal(source)
	body, bridge, err := PrepareWithCatalog(raw, "trusted-scope", replay, cache)
	require.NoError(t, err)
	require.Empty(t, bridge.tools)
	require.Contains(t, string(body), "compaction_trigger")
	require.Contains(t, string(body), "retain exact history")
	follow := []byte(`{"model":"gpt-6-astra","input":[{"type":"compaction","encrypted_content":"opaque"},{"role":"user","content":"use lookup"}]}`)
	_, bridge, err = PrepareWithCatalog(follow, "trusted-scope", replay, cache)
	require.NoError(t, err)
	require.Contains(t, bridge.tools, "lookup")
	require.Equal(t, raw, mustMarshalForCapabilityTest(t, source))
}
func mustMarshalForCapabilityTest(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	return raw
}

func TestReadCompactionReconstructsExactWindowAndRejectsTools(t *testing.T) {
	message := object{"type": "message", "role": "user", "id": "msg_keep", "content": []any{object{"type": "input_text", "text": "retain exactly"}}}
	state := object{"type": "compaction", "id": "cmp_keep", "encrypted_content": "opaque-state"}
	wire := sse(object{"type": "response.output_item.done", "output_index": 0, "item": message}) + sse(object{"type": "response.output_item.done", "output_index": 1, "item": state}) + sse(object{"type": "response.completed", "response": object{"id": "resp_keep", "status": "completed", "usage": object{"input_tokens": 23}}})
	response, err := ReadCompaction(strings.NewReader(wire), nil)
	require.NoError(t, err)
	require.JSONEq(t, `[{"type":"message","role":"user","id":"msg_keep","content":[{"type":"input_text","text":"retain exactly"}]},{"type":"compaction","id":"cmp_keep","encrypted_content":"opaque-state"}]`, string(mustMarshalForCapabilityTest(t, response["output"])))
	for _, failed := range []string{
		sse(object{"type": "response.output_item.done", "output_index": 0, "item": object{"type": "function_call", "name": "exec"}}),
		sse(object{"type": "response.completed", "response": object{"status": "completed", "output": []any{object{"type": "function_call", "name": "exec"}, state}}}),
		sse(object{"type": "response.incomplete", "response": object{"status": "incomplete", "output": []any{state}}}),
		sse(object{"type": "response.output_item.done", "output_index": 0, "item": state}),
	} {
		_, err = ReadCompaction(bytes.NewBufferString(failed), nil)
		require.Error(t, err)
	}
}

func TestEncryptedTransportArgumentsNeverBecomePlaintextCollaboration(t *testing.T) {
	source := testSource()
	source["tools"] = []any{object{"type": "namespace", "name": "collaboration", "tools": []any{object{"type": "function", "name": "send_message", "parameters": object{"type": "object", "properties": object{"message": object{"type": "string"}, "target": object{"type": "string"}}}}}}}
	_, bridge := mustPrepare(t, source, "scope", nil)
	native := nativeCall(object{"name": "collaboration.send_message", "arguments": object{"message": "looks readable", "target": "worker"}})
	native["encrypted_function_args"] = []any{"code"}
	call, err := bridge.translateCall(native)
	require.Error(t, err)
	require.Nil(t, call)
	native["encrypted_function_args"] = []any{}
	call, err = bridge.translateCall(native)
	require.NoError(t, err)
	require.Equal(t, []string{}, call["encrypted_function_args"])
}
