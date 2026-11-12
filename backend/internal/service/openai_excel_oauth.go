package service

import (
	"encoding/base64"
	"net/http"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/tidwall/gjson"
)

// IsExcelOAuth identifies the grant itself, not retired BPS test/automation state.
func (a *Account) IsExcelOAuth() bool {
	return a != nil && a.IsOpenAIOAuth() && strings.TrimSpace(a.GetCredential("client_id")) == openai.ExcelClientID
}

// excelOAuthClaims decodes only for consistency checking of tokens received
// from the trusted OAuth exchange. It does not authenticate arbitrary JWTs.
func excelOAuthClaims(token string) gjson.Result {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return gjson.Result{}
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !gjson.ValidBytes(raw) {
		return gjson.Result{}
	}
	return gjson.ParseBytes(raw)
}

func excelOAuthAccountID(token string) string {
	return strings.TrimSpace(excelOAuthClaims(token).Get("https://api\\.openai\\.com/auth.chatgpt_account_id").String())
}

func validateExcelOAuthTokenResponse(token *openai.TokenResponse) error {
	bad := func(message string) error {
		return infraerrors.New(http.StatusBadRequest, "OPENAI_EXCEL_OAUTH_INVALID_GRANT", message)
	}
	if token == nil || strings.TrimSpace(token.AccessToken) == "" {
		return bad("BPS OAuth did not return an access token")
	}
	claims := excelOAuthClaims(token.AccessToken)
	client := claims.Get("client_id").String()
	if client == "" {
		client = claims.Get("azp").String()
	}
	if client != openai.ExcelClientID {
		return bad("BPS requires an official Excel OAuth grant; Codex tokens cannot be converted")
	}
	if claims.Get("exp").Int() <= time.Now().Unix() || excelOAuthAccountID(token.AccessToken) == "" {
		return bad("BPS OAuth token is expired or does not identify a ChatGPT workspace")
	}
	if token.IDToken != "" {
		aud := excelOAuthClaims(token.IDToken).Get("aud")
		matched := aud.Type == gjson.String && aud.String() == openai.ExcelClientID
		for _, value := range aud.Array() {
			matched = matched || value.String() == openai.ExcelClientID
		}
		if !matched {
			return bad("BPS OAuth ID token belongs to a different client")
		}
		if id := excelOAuthAccountID(token.IDToken); id != "" && id != excelOAuthAccountID(token.AccessToken) {
			return bad("BPS OAuth tokens identify different workspaces")
		}
	}
	return nil
}

func populateExcelOAuthTokenInfo(info *OpenAITokenInfo) {
	claims := excelOAuthClaims(info.AccessToken)
	info.ChatGPTAccountID = excelOAuthAccountID(info.AccessToken)
	auth := claims.Get("https://api\\.openai\\.com/auth")
	if info.ChatGPTUserID == "" {
		info.ChatGPTUserID = auth.Get("chatgpt_user_id").String()
	}
	if info.PlanType == "" {
		info.PlanType = auth.Get("chatgpt_plan_type").String()
	}
	if info.Email == "" {
		info.Email = claims.Get("https://api\\.openai\\.com/profile.email").String()
	}
}

func validateExcelOAuthAccount(account *Account, accessToken string) error {
	if err := validateExcelOAuthTokenResponse(&openai.TokenResponse{AccessToken: accessToken}); err != nil {
		return err
	}
	if id := strings.TrimSpace(account.GetCredential("chatgpt_account_id")); id != "" && id != excelOAuthAccountID(accessToken) {
		return infraerrors.New(http.StatusBadRequest, "OPENAI_EXCEL_OAUTH_WORKSPACE_MISMATCH", "BPS OAuth account and access token identify different workspaces")
	}
	if strings.EqualFold(strings.TrimSpace(account.GetCredential("plan_type")), "free") {
		return infraerrors.New(http.StatusForbidden, "OPENAI_EXCEL_OAUTH_PLAN_UNSUPPORTED", "BPS is unavailable for Free accounts; upstream model access is required")
	}
	return nil
}
