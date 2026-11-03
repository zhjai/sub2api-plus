package cpaeval

import "testing"

func TestHasStandalone21(t *testing.T) {
	for text, want := range map[string]bool{
		"21": true, "最少取出**21**个": true, "答案：21。": true,
		"121": false, "210": false, "2 1": false,
	} {
		if got := hasStandalone21(text); got != want {
			t.Errorf("hasStandalone21(%q) = %v, want %v", text, got, want)
		}
	}
}
