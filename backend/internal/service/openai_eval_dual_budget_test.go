//go:build unit

package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestOpenAIEvalDualGrantControllerPinsChannelAndSharesWorkspaceBudget(t *testing.T) {
	a := dualBPSEvalFixture(t)
	svc, _, _ := evalRunHarness(t, nil)
	svc.accounts = &openAIAccountTestRepo{mockAccountRepoForGemini: mockAccountRepoForGemini{accountsByID: map[int64]*Account{a.ID: a}}}
	credential := a.BPSAccountForModel("alias")
	require.NotNil(t, credential)
	require.Equal(t, openAIEvalCredentialNamespace(a), openAIEvalCredentialNamespace(credential), "dual grants must not bypass aggregate workspace budget")
	c := &openAIEvalController{service: svc, credential: credential, namespace: openAIEvalCredentialNamespace(credential), accountID: a.ID, requestedModel: "alias"}
	// Route changes are rejected before control lookup/admission can send.
	a.Extra[ExcelBPSEnabledKey] = false
	_, err := c.current(context.Background())
	require.ErrorContains(t, err, "credential_namespace_changed")
}
