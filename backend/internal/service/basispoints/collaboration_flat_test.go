package basispoints

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestFlatCollaborationKeepsDeclaredIdentityAndEncryptionBoundary(t *testing.T) {
	for _, namespace := range []string{"", "functions"} {
		for _, prefix := range []string{"collaboration.", "collaboration__"} {
			for _, leaf := range []string{"spawn_agent", "send_message", "followup_task"} {
				name := prefix + leaf
				t.Run(namespace+"/"+name, func(t *testing.T) {
					source := collaborationSource(leaf)
					declaration := source["tools"].([]any)[0].(object)["tools"].([]any)[0].(object)
					declaration["name"] = name
					source["tools"] = []any{declaration}
					if namespace != "" {
						source["tools"] = []any{object{"type": "namespace", "name": namespace, "tools": []any{declaration}}}
					}
					key := name
					if namespace != "" {
						key = namespace + "." + name
					}
					_, bridge := mustPrepare(t, source, "flat/parent", new(ReplayCache))
					info := bridge.tools[key]
					prompt := plaintextPromptParameters(info.Name, info.Namespace, info.Parameters)
					if _, exists := prompt["properties"].(object)["message"].(object)["encrypted"]; exists {
						t.Fatal("flat collaboration schema still advertises encrypted messages")
					}
					if info.Parameters["properties"].(object)["message"].(object)["encrypted"] != true {
						t.Fatal("prompt adaptation mutated the original schema")
					}
					args := object{"message": "preserve the original task"}
					for _, direct := range []bool{false, true} {
						call := nativeCall(object{"name": key, "arguments": args})
						if direct {
							raw, _ := json.Marshal(args)
							call["name"], call["arguments"] = key, string(raw)
						}
						result, err := bridge.translateCall(call)
						if err != nil {
							t.Fatal(err)
						}
						requirePlaintextArguments(t, result)
						if result["name"] != name || text(result["namespace"]) != namespace {
							t.Fatalf("declared flat identity changed: %v", result)
						}
						var actual object
						if decode([]byte(text(result["arguments"])), &actual) != nil || !reflect.DeepEqual(actual, args) {
							t.Fatal("task changed")
						}
						if _, err := bridge.rebuildNativeHistoryCall(result); err != nil {
							t.Fatal(err)
						}
					}
					for _, metadata := range []any{[]any{"message"}, "message", true} {
						encrypted := object{"type": "function_call", "name": key, "call_id": "call_encrypted", "arguments": "{\"message\":\"PRIVATE_TASK\"}", "encrypted_function_args": metadata}
						result, err := bridge.translateCall(encrypted)
						if result != nil || err == nil || strings.Contains(err.Error(), "PRIVATE_TASK") {
							t.Fatalf("flat encrypted assignment escaped: %v %v", result, err)
						}
					}
				})
			}
		}
	}

}

func TestPlaintextCollaborationDoesNotClaimOtherNamespaces(t *testing.T) {
	for _, item := range [][2]string{{"spawn_agent", "other"}, {"collaboration.spawn_agent", "other"}, {"collaboration__spawn_agent", "other"}, {"collaboration.spawn_agent_extra", ""}, {"collaboration__send_message_extra", ""}} {
		if plaintextCollaborationTool(item[0], item[1]) {
			t.Fatalf("claimed unrelated tool %v", item)
		}
	}
}
