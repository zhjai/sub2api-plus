//go:build unit

package service

import (
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
)

func TestRetiredPrismCannotBeScheduledOrForwarded(t *testing.T) {
	account := &Account{ID: 42, Platform: PlatformPrism, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true}
	require.False(t, account.IsSchedulable())
	require.False(t, account.IsOpenAICompatible())
	require.False(t, account.IsModelSupported("gpt-6-astra"))
	require.False(t, isConcreteRequestPlatform(PlatformPrism))
	require.Equal(t, PlatformPrism, NormalizeOpenAICompatiblePlatform(PlatformPrism))
	// Reject before consulting any gateway dependencies or sending credentials.
	gateway := &OpenAIGatewayService{}
	_, err := gateway.Forward(t.Context(), nil, account, nil)
	require.ErrorContains(t, err, "Prism channel has been removed")
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	_, err = gateway.ForwardAsChatCompletions(t.Context(), c, account, nil, "", "")
	require.ErrorContains(t, err, "Prism channel has been removed")
}

func TestRetiredPrismCannotBeCreated(t *testing.T) {
	admin := &adminServiceImpl{}
	_, err := admin.CreateAccount(t.Context(), &CreateAccountInput{Platform: PlatformPrism})
	require.ErrorContains(t, err, "Prism channel has been removed")
	_, err = admin.CreateGroup(t.Context(), &CreateGroupInput{Platform: PlatformPrism})
	require.ErrorContains(t, err, "Prism channel has been removed")
	_, err = admin.UpdateGroup(t.Context(), 42, &UpdateGroupInput{Platform: PlatformPrism})
	require.ErrorContains(t, err, "Prism channel has been removed")
	require.Empty(t, defaultModelsListCandidateIDs(PlatformPrism))
}

func TestNamedPrismAPIAccountRemainsSupported(t *testing.T) {
	account := &Account{ID: 43, Name: "prism", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true}
	require.True(t, account.IsSchedulable())
	require.True(t, account.IsOpenAICompatible())
	require.True(t, account.IsModelSupported("gpt-6-astra"))
}
