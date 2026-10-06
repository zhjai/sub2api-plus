package service

import (
	"context"
	"strings"
	"testing"
)

func TestPrismConnectivityUnsupportedModesNeverReachUpstream(t *testing.T) {
	s := &AccountTestService{}
	for _, mode := range []string{"compact", "image", "audio", "video", "realtime"} {
		c, w := prismTestContext("")
		err := s.testPrismAccountConnection(c, &Account{Platform: PlatformPrism}, "", "", mode, AccountTestOptions{})
		if err == nil || !strings.Contains(w.Body.String(), "unsupported_mode") || strings.Contains(w.Body.String(), "test_complete") {
			t.Fatalf("mode=%s: err=%v output=%s", mode, err, w.Body.String())
		}
	}
	c, w := prismTestContext("")
	if s.testPrismAccountConnection(c, &Account{Platform: PlatformPrism}, "", "", "text", AccountTestOptions{ImageDataURL: "data:image/png;base64,AA=="}) == nil || !strings.Contains(w.Body.String(), "unsupported_mode") {
		t.Fatal("text mode accepted media")
	}
}

func TestPrismConnectivityCancellationIsNotSuccess(t *testing.T) {
	c, w := prismTestContext("")
	ctx, cancel := context.WithCancel(c.Request.Context())
	cancel()
	c.Request = c.Request.WithContext(ctx)
	err := (&AccountTestService{}).testPrismAccountConnection(c, &Account{Platform: PlatformPrism}, "", "", "default", AccountTestOptions{})
	if err == nil || !strings.Contains(w.Body.String(), "canceled") || strings.Contains(w.Body.String(), "test_complete") {
		t.Fatalf("err=%v output=%s", err, w.Body.String())
	}
}
