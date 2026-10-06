package bridge

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/prismbridge/prism"
)

func TestCompleteHistoryAndToolResultRetained(t *testing.T) {
	result := strings.Repeat("long-result-", 800)
	body, _ := json.Marshal(map[string]any{"model": "test", "tools": []any{map[string]any{"type": "custom", "name": "exec"}}, "input": []any{map[string]any{"role": "user", "content": "first secret"}, map[string]any{"type": "custom_tool_call", "call_id": "call_1", "name": "exec", "input": "text(await tools.exec_command({cmd:'pwd'}))"}, map[string]any{"type": "custom_tool_call_output", "call_id": "call_1", "output": result}, map[string]any{"role": "user", "content": "remember all"}}})
	r, err := Parse(body)
	if err != nil {
		t.Fatal(err)
	}
	items, _ := InputItems(r.Input)
	input, on, err := Prepare(r, items)
	if err != nil || !on {
		t.Fatalf("prepare %v %v", on, err)
	}
	text := input[1].Content[0].Text
	for _, want := range []string{"first secret", "call_1", result, "remember all"} {
		if !strings.Contains(text, want) {
			t.Fatalf("lost input of length %d", len(want))
		}
	}
}

func TestOversizeRejectedWithoutTruncation(t *testing.T) {
	b, _ := json.Marshal(map[string]any{"model": "m", "input": strings.Repeat("x", PromptLimit)})
	r, _ := Parse(b)
	items, _ := InputItems(r.Input)
	_, _, err := Prepare(r, items)
	if err == nil || !strings.Contains(err.Error(), "context_length_exceeded") {
		t.Fatal(err)
	}
}
func TestCompactAndImagesRejected(t *testing.T) {
	for _, body := range []string{`{"model":"m","input":"hello","context_management":[{"type":"compaction"}]}`, `{"model":"m","input":[{"role":"user","content":[{"type":"input_image","image_url":"https://example.com/a"}]}]}`, `{"model":"m","input":"x","truncation":"auto"}`} {
		if _, err := Parse([]byte(body)); err == nil {
			t.Fatalf("accepted %s", body)
		}
	}
}
func TestBridgeToolKindsAndIncompleteFence(t *testing.T) {
	for _, kind := range []string{"custom", "function"} {
		r, _ := Parse([]byte(`{"model":"m","input":"x","tools":[{"type":"` + kind + `","name":"exec_command"}]}`))
		items, _ := InputItems(r.Input)
		_, on, err := Prepare(r, items)
		if err != nil {
			t.Fatal(err)
		}
		out, err := Output(r, "```codex-exec\nconst out = await tools.exec_command({cmd: \"pwd\"});\ntext(out);\n```", on, "one")
		if err != nil {
			t.Fatal(err)
		}
		want := "custom_tool_call"
		if kind == "function" {
			want = "function_call"
		}
		if !strings.Contains(string(out[0]), `"type":"`+want+`"`) {
			t.Fatal(string(out[0]))
		}
		if _, err := Output(r, "```codex-exec\necho incomplete", on, "two"); err == nil {
			t.Fatal("incomplete fence accepted")
		}
	}
}
func TestGenericToolsRejectedBeforeRun(t *testing.T) {
	r, _ := Parse([]byte(`{"model":"m","input":"hello","tools":[{"type":"function","name":"weather"}]}`))
	items, _ := InputItems(r.Input)
	if _, _, err := Prepare(r, items); err == nil {
		t.Fatal("generic tools accepted")
	}
}
func TestFunctionBridgeCannotDropMultipleCalls(t *testing.T) {
	r, _ := Parse([]byte(`{"model":"m","input":"x","tools":[{"type":"function","name":"exec_command"}]}`))
	if _, err := Output(r, "```codex-exec\nawait tools.exec_command({cmd:'one'}); await tools.exec_command({cmd:'two'});\n```", true, "x"); err == nil {
		t.Fatal("lost second call")
	}
	if _, err := Output(r, "```codex-exec\nawait tools.exec_command({cmd:'one'}); await tools.write_stdin({session_id:2,chars:'x'});\n```", true, "x"); err == nil {
		t.Fatal("lost non-exec tool operation")
	}
}

func TestCompactMetadataRejected(t *testing.T) {
	if !IsCompactMetadata(`{"request_kind":"compaction","strategy":"memento"}`) {
		t.Fatal("header compact missed")
	}
	body := []byte(`{"model":"m","input":"x","client_metadata":{"x-codex-turn-metadata":"{\"request_kind\":\"compaction\"}"}}`)
	if _, err := Parse(body); err == nil {
		t.Fatal("body compact accepted")
	}
}

func TestUnsafeDeltaPathsNeverBecomeCommands(t *testing.T) {
	for _, path := range []string{"../escape", "/etc/passwd", "C:\\temp\\file", "x\ncommand"} {
		if _, err := safeRelPath(path); err == nil {
			t.Fatalf("accepted %q", path)
		}
	}
}

func TestDeltaModifiedFileIsContextCheckedPatch(t *testing.T) {
	f := prism.CodexDeltaFile{FilePath: "src/file.txt", Status: "modified", Diff: json.RawMessage(`{"version":1,"hunks":[{"original":"old content","updated":"new content","location":{"originalStartLine":2,"originalLineCount":1}}]}`)}
	plan, err := planDeltaFile(f)
	if err != nil || plan.Kind != editPatch || len(plan.Hunks) != 1 || plan.Hunks[0].Old != "old content" || plan.Hunks[0].New != "new content" {
		t.Fatalf("%+v %v", plan, err)
	}
	js := SynthesizeDeltaFilesExecJS([]prism.CodexDeltaFile{f}, false)
	if !strings.Contains(js, "tools.exec_command") || strings.Contains(js, "base64 --decode >") {
		t.Fatal("modified file must not become whole-file overwrite")
	}
}
