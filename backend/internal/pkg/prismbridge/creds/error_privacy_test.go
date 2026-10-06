package creds

import (
	"strings"
	"testing"
	"time"
)

func TestAPIErrorKeepsSensitiveBodyOutOfErrorString(t *testing.T) {
	for _, delay := range []time.Duration{0, time.Second} {
		err := &APIError{Op: "session", Status: 403, Body: "Request verification failed; access_token=secret", RetryAfter: delay}
		if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "access_token") {
			t.Fatalf("upstream body escaped through error: %s", err.Error())
		}
		if IsAuthError(err) {
			t.Fatal("privacy protection must preserve Sentinel failure classification")
		}
	}
}
