package urlvalidator

import "testing"

func TestValidateHTTPSOrigin(t *testing.T) {
	for _, raw := range []string{"https://images.example", "https://images.example/", "https://images.example:8443"} {
		if err := ValidateHTTPSOrigin(raw); err != nil {
			t.Errorf("valid origin %q: %v", raw, err)
		}
	}
	for _, raw := range []string{"http://images.example", "https://user:pass@images.example", "https://images.example/path", "https://images.example?", "https://images.example#", "https://127.0.0.1", "https://localhost", "https://[::1]"} {
		if err := ValidateHTTPSOrigin(raw); err == nil {
			t.Errorf("invalid origin accepted: %q", raw)
		}
	}
}
