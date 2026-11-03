package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/service/cpaeval"
	"strings"
	"time"
)

const pelicanFrameTokenPrefix = "sub2api:pelican-frame:v1:"
const PelicanFrameCSP = "sandbox allow-scripts; default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data:; font-src data:; media-src data:; connect-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'"

const PelicanPreviewFreeze = cpaeval.PelicanPreviewFreeze

type OpenAIEvalArtifactRepository interface {
	GetPelicanArtifact(context.Context, int64) (string, error)
}

type pelicanFrameClaims struct {
	Version int    `json:"v"`
	RunID   int64  `json:"run_id"`
	Mode    string `json:"mode"`
	Expires int64  `json:"exp"`
}

func (s *OpenAIEvalService) pelicanEncryptor() SecretEncryptor {
	if s == nil || s.accountTest == nil || s.accountTest.pluginManager == nil {
		return nil
	}
	return s.accountTest.pluginManager.encryptor
}

func (s *OpenAIEvalService) CreatePelicanFrameToken(ctx context.Context, runID int64, mode string) (string, time.Time, error) {
	if runID <= 0 || (mode != "preview" && mode != "play") {
		return "", time.Time{}, errors.New("invalid Pelican preview request")
	}
	repo, ok := s.repo.(OpenAIEvalArtifactRepository)
	if !ok || s.pelicanEncryptor() == nil {
		return "", time.Time{}, errors.New("Pelican preview is unavailable")
	}
	if _, err := repo.GetPelicanArtifact(ctx, runID); err != nil {
		return "", time.Time{}, err
	}
	expires := time.Now().Add(5 * time.Minute)
	payload, err := json.Marshal(pelicanFrameClaims{1, runID, mode, expires.Unix()})
	if err != nil {
		return "", time.Time{}, err
	}
	encrypted, err := s.pelicanEncryptor().Encrypt(pelicanFrameTokenPrefix + string(payload))
	if err != nil {
		return "", time.Time{}, err
	}
	return base64.RawURLEncoding.EncodeToString([]byte(encrypted)), expires, nil
}

func (s *OpenAIEvalService) ResolvePelicanFrame(ctx context.Context, token string) (string, error) {
	if len(token) == 0 || len(token) > 4096 || s.pelicanEncryptor() == nil {
		return "", errors.New("invalid Pelican preview token")
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return "", errors.New("invalid Pelican preview token")
	}
	plaintext, err := s.pelicanEncryptor().Decrypt(string(raw))
	if err != nil {
		return "", errors.New("invalid Pelican preview token")
	}
	payload, ok := strings.CutPrefix(plaintext, pelicanFrameTokenPrefix)
	if !ok {
		return "", errors.New("invalid Pelican preview token")
	}
	var claims pelicanFrameClaims
	if err = json.Unmarshal([]byte(payload), &claims); err != nil || claims.Version != 1 || claims.RunID <= 0 || (claims.Mode != "preview" && claims.Mode != "play") || time.Now().Unix() >= claims.Expires || claims.Expires > time.Now().Add(5*time.Minute).Unix() {
		return "", errors.New("invalid or expired Pelican preview token")
	}
	repo, ok := s.repo.(OpenAIEvalArtifactRepository)
	if !ok {
		return "", errors.New("Pelican preview is unavailable")
	}
	html, err := repo.GetPelicanArtifact(ctx, claims.RunID)
	if err != nil {
		return "", err
	}
	if len(html) > 300000 {
		return "", errors.New("Pelican preview exceeds size limit")
	}
	if claims.Mode == "preview" {
		html += PelicanPreviewFreeze
	}
	return html, nil
}
