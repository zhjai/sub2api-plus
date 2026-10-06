package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSchedulerDecisionsValidatesGroupFilter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/decisions", (&AccountHandler{}).GetSchedulerDecisions)
	for _, raw := range []string{"0", "-1", "abc", "7.5", "9223372036854775808"} {
		t.Run(raw, func(t *testing.T) {
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/decisions?group_id="+raw, nil))
			require.Equal(t, http.StatusBadRequest, response.Code)
			require.Contains(t, response.Body.String(), "group_id")
		})
	}
	for _, query := range []string{"", "?group_id=7", "?group_id=7&limit=1"} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/decisions"+query, nil))
		require.Equal(t, http.StatusOK, response.Code)
	}
}
