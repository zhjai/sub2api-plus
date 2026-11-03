package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

type pelicanArtifactTestRepo struct {
	OpenAIEvalRepository
	pages map[int64]string
}

func (r pelicanArtifactTestRepo) GetPelicanArtifact(_ context.Context, id int64) (string, error) {
	if p, ok := r.pages[id]; ok {
		return p, nil
	}
	return "", errors.New("not found")
}

type pelicanTestEncryptor struct{}

func (pelicanTestEncryptor) Encrypt(p string) (string, error) { return "cipher:" + p, nil }
func (pelicanTestEncryptor) Decrypt(c string) (string, error) {
	if p, ok := strings.CutPrefix(c, "cipher:"); ok {
		return p, nil
	}
	return "", errors.New("bad cipher")
}

func TestPelicanFrameCapabilityAndIsolation(t *testing.T) {
	s := NewOpenAIEvalService(pelicanArtifactTestRepo{pages: map[int64]string{7: "<!doctype html><script>animate()</script>"}}, nil, &AccountTestService{pluginManager: &PluginManager{encryptor: pelicanTestEncryptor{}}})
	ctx := t.Context()
	for _, mode := range []string{"preview", "play"} {
		token, expires, err := s.CreatePelicanFrameToken(ctx, 7, mode)
		if err != nil {
			t.Fatal(err)
		}
		if time.Until(expires) > 5*time.Minute {
			t.Fatal("unbounded token")
		}
		html, err := s.ResolvePelicanFrame(ctx, token)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(html, PelicanPreviewFreeze) != (mode == "preview") {
			t.Fatal("preview freeze contract")
		}
	}
	if _, _, err := s.CreatePelicanFrameToken(ctx, 8, "play"); err == nil {
		t.Fatal("missing/non-Pelican run authorized")
	}
	if _, _, err := s.CreatePelicanFrameToken(ctx, 7, "anything"); err == nil {
		t.Fatal("invalid mode authorized")
	}
	for _, claims := range []pelicanFrameClaims{{1, 7, "play", time.Now().Add(-time.Second).Unix()}, {1, 7, "play", time.Now().Add(time.Hour).Unix()}, {1, 7, "escape", time.Now().Add(time.Minute).Unix()}, {1, 8, "play", time.Now().Add(time.Minute).Unix()}} {
		raw, _ := json.Marshal(claims)
		enc, _ := pelicanTestEncryptor{}.Encrypt(pelicanFrameTokenPrefix + string(raw))
		token := base64.RawURLEncoding.EncodeToString([]byte(enc))
		if _, err := s.ResolvePelicanFrame(ctx, token); err == nil {
			t.Fatalf("invalid claims accepted: %+v", claims)
		}
	}
	for _, token := range []string{"bad", strings.Repeat("x", 4097), base64.RawURLEncoding.EncodeToString([]byte("cipher:another-purpose:{}"))} {
		if _, err := s.ResolvePelicanFrame(ctx, token); err == nil {
			t.Fatal("invalid token accepted")
		}
	}
	for _, required := range []string{"sandbox allow-scripts", "default-src 'none'", "connect-src 'none'", "base-uri 'none'", "form-action 'none'"} {
		if !strings.Contains(PelicanFrameCSP, required) {
			t.Fatal("missing CSP", required)
		}
	}
	if strings.Contains(PelicanFrameCSP, "allow-same-origin") || strings.Contains(PelicanFrameCSP, "https:") {
		t.Fatal("frame can access application origin/network")
	}
}
