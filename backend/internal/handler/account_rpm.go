package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const maxAccountRPMVetoes = 10
const accountRPMVetoKey = "account_rpm_local_vetoes"

type accountRPMVetoState struct {
	accounts map[int64]struct{}
	count    int
	last     *service.AccountRPMError
}

func accountRPMVetoes(c *gin.Context) *accountRPMVetoState {
	if v, ok := c.Get(accountRPMVetoKey); ok {
		return v.(*accountRPMVetoState)
	}
	v := &accountRPMVetoState{accounts: make(map[int64]struct{})}
	c.Set(accountRPMVetoKey, v)
	return v
}

// Local capacity has its own bounded budget and never enters upstream failover.
// Call after releasing concurrency, before health, integrity or usage callbacks.
func handleAccountRPMError(c *gin.Context, err error, excluded map[int64]struct{}, movable, outputStarted bool) (handled, retry bool) {
	var local *service.AccountRPMError
	if !errors.As(err, &local) {
		return false, false
	}
	if c.Request.Context().Err() != nil {
		return true, false
	}
	if movable && !outputStarted && !local.Unavailable && !local.NoMigration && excluded != nil {
		state := accountRPMVetoes(c)
		state.count++
		state.accounts[local.AccountID] = struct{}{}
		excluded[local.AccountID] = struct{}{}
		if state.last == nil || local.RetryAfterSeconds() < state.last.RetryAfterSeconds() {
			state.last = local
		}
		if state.count < maxAccountRPMVetoes {
			return true, true
		}
	}
	writeAccountRPMError(c, local)
	return true, false
}

// Mixed upstream/profit exclusions retain the existing exhausted response.
func accountRPMSelectionExhausted(c *gin.Context, excluded map[int64]struct{}) bool {
	local := accountRPMSelectionError(c, excluded)
	if local == nil {
		return false
	}
	writeAccountRPMError(c, local)
	return true
}

func accountRPMSelectionError(c *gin.Context, excluded map[int64]struct{}) *service.AccountRPMError {
	v, ok := c.Get(accountRPMVetoKey)
	if !ok || len(excluded) == 0 {
		return nil
	}
	state := v.(*accountRPMVetoState)
	for id := range excluded {
		if _, ok := state.accounts[id]; !ok {
			return nil
		}
	}
	return state.last
}

func writeAccountRPMError(c *gin.Context, local *service.AccountRPMError) {
	if local == nil || c.Request.Context().Err() != nil {
		return
	}
	kind := "rate_limit_error"
	if local.Unavailable {
		kind = "service_unavailable"
	}
	body := gin.H{"error": gin.H{"type": kind, "code": local.Code(), "message": local.Error()}}
	path := c.Request.URL.Path
	if strings.Contains(path, "/messages") {
		body["type"] = "error"
	}
	if strings.Contains(path, "/v1beta/") {
		status := "RESOURCE_EXHAUSTED"
		if local.Unavailable {
			status = "UNAVAILABLE"
		}
		body = gin.H{"error": gin.H{"code": local.StatusCode(), "message": local.Error(), "status": status}}
	}
	if c.Writer.Written() {
		if strings.Contains(c.Writer.Header().Get("Content-Type"), "text/event-stream") {
			data, _ := json.Marshal(body)
			_, _ = fmt.Fprintf(c.Writer, "event: error\ndata: %s\n\n", data)
			c.Writer.Flush()
		}
		return
	}
	if !local.Unavailable {
		c.Header("Retry-After", strconv.Itoa(local.RetryAfterSeconds()))
	}
	c.JSON(local.StatusCode(), body)
}

// Selection hints are advisory; only the send-boundary check consumes capacity.
// Keep vetoes through the existing selection-backoff reset without spending its budget.
func (s *FailoverState) handleAccountRPMError(c *gin.Context, err error, outputStarted bool) (bool, bool) {
	handled, retry := handleAccountRPMError(c, err, s.FailedAccountIDs, true, outputStarted)
	if retry {
		s.rpmVetoedAccountIDs = accountRPMVetoes(c).accounts
	}
	return handled, retry
}

func (s *FailoverState) allExclusionsAreLocalVetoed() bool {
	if len(s.FailedAccountIDs) == 0 {
		return false
	}
	for id := range s.FailedAccountIDs {
		if _, ok := s.rpmVetoedAccountIDs[id]; ok {
			continue
		}
		if _, ok := s.profitVetoedAccountIDs[id]; !ok {
			return false
		}
	}
	return true
}
