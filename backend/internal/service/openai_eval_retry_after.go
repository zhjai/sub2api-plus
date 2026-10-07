package service

import (
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Accept Retry-After seconds and HTTP-date without allowing float overflow,
// NaN or negative values to turn a required wait into an immediate retry.
func openAIEvalRetryAfter(headers http.Header, now time.Time) time.Duration {
	raw := strings.TrimSpace(headers.Get("Retry-After"))
	if raw == "" {
		return 0
	}
	if seconds, err := strconv.ParseFloat(raw, 64); err == nil {
		if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds <= 0 {
			return 0
		}
		if seconds >= float64(time.Duration(1<<63-1))/float64(time.Second) {
			return time.Duration(1<<63 - 1)
		}
		return time.Duration(seconds * float64(time.Second))
	}
	if reset, err := http.ParseTime(raw); err == nil && reset.After(now) {
		return reset.Sub(now)
	}
	return 0
}
