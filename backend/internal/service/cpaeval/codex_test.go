package cpaeval

import (
	"encoding/hex"
	"reflect"
	"strings"
	"testing"
)

func TestCodexPrefixMatchesCapturedThread(t *testing.T) {
	var thread uuid
	raw, _ := hex.DecodeString(strings.ReplaceAll("01a152e2-654d-7e50-8c11-f55f17d42b98", "-", ""))
	copy(thread[:], raw)
	prefix := codexPrefix(thread)
	if prefix[0].ID != "at_e223b652-6670-5575-bae9-4a24ffc36fb5" || prefix[1].ID != "msg_5610970c-beaa-524d-8bb6-7985bec433b0" {
		t.Fatalf("prefix IDs = %s, %s", prefix[0].ID, prefix[1].ID)
	}
}

func TestCodexTurn(t *testing.T) {
	request, headers := codexTurn("auth", "gpt-5.5", "low", "", "q")
	thread, metadata := request.PromptCacheKey, request.ClientMetadata
	if headers.Get("Session-Id") != thread || headers.Get("Thread-Id") != thread || metadata.ThreadID != thread ||
		headers.Get("X-Codex-Turn-Metadata") != metadata.TurnMetadata || request.Input[len(request.Input)-1].Content[0].Text != "q" {
		t.Fatalf("inconsistent turn: headers = %v, metadata = %+v", headers, metadata)
	}
	again, _ := codexTurn("auth", "gpt-5.5", "low", "", "q")
	other, _ := codexTurn("other", "gpt-5.5", "low", "", "q")
	if again.PromptCacheKey == thread || again.ClientMetadata.InstallationID != metadata.InstallationID || other.ClientMetadata.InstallationID == metadata.InstallationID {
		t.Fatal("each turn needs a new thread on its credential's stable installation")
	}
}

func TestCodexTurnDeveloperInstructions(t *testing.T) {
	plain, _ := codexTurn("auth", "gpt-5.5", "", "", "q")
	instructed, _ := codexTurn("auth", "gpt-5.5", "", "Answer directly.", "q")
	context, first := plain.Input[2], instructed.Input[2]
	if len(instructed.Input) != len(plain.Input) || first.Content[0].Text != "Answer directly." || first.Metadata.ContentItemKinds[0] != "generic.developer_instructions" ||
		!reflect.DeepEqual(first.Content[1:], context.Content) || !reflect.DeepEqual(first.Metadata.ContentItemKinds[1:], context.Metadata.ContentItemKinds) {
		t.Fatalf("developer instructions must lead the first context message: %+v", first)
	}
}
