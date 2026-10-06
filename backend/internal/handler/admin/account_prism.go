package admin

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func prismAdminError(c *gin.Context, err error) {
	var pe *service.PrismAccountError
	if errors.As(err, &pe) {
		status := http.StatusBadRequest
		switch pe.Code {
		case "upstream_error", "timeout":
			status = http.StatusBadGateway
		case "rate_limited":
			status = http.StatusTooManyRequests
		case "storage_error", "storage_unavailable":
			status = http.StatusInternalServerError
		}
		c.JSON(status, gin.H{"code": pe.Code, "message": pe.Message})
		return
	}
	response.InternalError(c, "Prism 操作失败")
}
func prismBind(c *gin.Context, v any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4<<20)
	if err := c.ShouldBindJSON(v); err != nil {
		response.BadRequest(c, "请求格式无效或超过 4 MiB 限制")
		return false
	}
	return true
}
func (h *AccountHandler) ImportPrismAccounts(c *gin.Context) {
	var req service.PrismImportRequest
	if !prismBind(c, &req) {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 150*time.Second)
	defer cancel()
	result, err := h.prismAccountService.Import(ctx, req)
	if err != nil {
		prismAdminError(c, err)
		return
	}
	response.Success(c, result)
}
func (h *AccountHandler) BeginPrismOAuth(c *gin.Context) {
	var req service.PrismImportRequest
	if !prismBind(c, &req) {
		return
	}
	result, err := h.prismAccountService.BeginOAuth(c.Request.Context(), req)
	if err != nil {
		prismAdminError(c, err)
		return
	}
	response.Success(c, result)
}
func (h *AccountHandler) ExchangePrismOAuth(c *gin.Context) {
	var req struct {
		SessionID   string `json:"session_id"`
		CallbackURL string `json:"callback_url"`
	}
	if !prismBind(c, &req) {
		return
	}
	result, err := h.prismAccountService.ExchangeOAuth(c.Request.Context(), req.SessionID, req.CallbackURL)
	if err != nil {
		prismAdminError(c, err)
		return
	}
	response.Success(c, result)
}
func (h *AccountHandler) PrismOAuthStatus(c *gin.Context) {
	result, err := h.prismAccountService.OAuthStatus(c.Query("session_id"))
	if err != nil {
		prismAdminError(c, err)
		return
	}
	response.Success(c, result)
}
func (h *AccountHandler) CancelPrismOAuth(c *gin.Context) {
	var req struct {
		SessionID string `json:"session_id"`
	}
	if !prismBind(c, &req) {
		return
	}
	result, err := h.prismAccountService.CancelOAuth(req.SessionID)
	if err != nil {
		prismAdminError(c, err)
		return
	}
	response.Success(c, result)
}
func prismAccountID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "无效账号 ID")
		return 0, false
	}
	return id, true
}
func (h *AccountHandler) RefreshPrismAccount(c *gin.Context) {
	id, ok := prismAccountID(c)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()
	result, err := h.prismAccountService.Refresh(ctx, id)
	if err != nil {
		prismAdminError(c, err)
		return
	}
	response.Success(c, result)
}
func (h *AccountHandler) PrismAccountModels(c *gin.Context) {
	id, ok := prismAccountID(c)
	if !ok {
		return
	}
	account, models, err := h.prismAccountService.CatalogSnapshot(c.Request.Context(), id, c.Query("refresh") == "true")
	if err != nil {
		prismAdminError(c, err)
		return
	}
	response.Success(c, gin.H{"models": models, "public_models": service.PrismPublicModels(account, models), "source": "prism_account_catalog"})
}
