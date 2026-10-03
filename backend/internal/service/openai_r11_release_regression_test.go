//go:build unit

package service

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestR11TerminatedIsInfrastructureFailure(t *testing.T) {
	for _, event := range []string{"response.done", "status_only"} {
		payload := []byte(`{"response":{"status":"terminated"}}`)
		status, raw, _ := classifyOpenAIResponsesOutcome(event, payload)
		require.Equal(t, "failed", status)
		require.Equal(t, "terminated", raw)
		require.False(t, (&OpenAIForwardResult{ResponsesOutcomeObserved: true, ResponsesProtocolStatus: status}).SucceededForScheduling())
	}
}

func TestR11WSExecOutputStage(t *testing.T) {
	contract := []byte(`{"tools":[{"type":"custom","name":"exec"}]}`)
	for _, control := range []string{"leak", "ordinary", "real_exec", "missing_client", "missing_outbound", "cancelled", "overflow"} {
		t.Run(control, func(t *testing.T) {
			client, outbound := contract, contract
			if control == "missing_client" {
				client = []byte(`{}`)
			}
			if control == "missing_outbound" {
				outbound = []byte(`{}`)
			}
			e := newOpenAIWSIntegrityEvidence(client, outbound)
			deliver := func(event, payload string) ([][]byte, error) {
				e.observe(event, []byte(payload))
				return e.prepareDelivery(event, []byte(payload))
			}
			frames, err := deliver("response.created", `{"type":"response.created","response":{"id":"resp_stage"}}`)
			require.NoError(t, err)
			if strings.HasPrefix(control, "missing_") {
				require.Len(t, frames, 1)
				return
			}
			require.Empty(t, frames)
			frames, err = deliver("keepalive", `{"type":"keepalive"}`)
			require.NoError(t, err)
			require.Len(t, frames, 1)
			e.delivered("keepalive", frames[0])
			if control == "ordinary" {
				frames, err = deliver("response.output_text.delta", `{"type":"response.output_text.delta","delta":"ordinary prose"}`)
				require.NoError(t, err)
				require.Len(t, frames, 2)
				e.delivered("response.output_text.delta", frames[1])
			}
			if control == "overflow" {
				_, err = deliver("response.created", fmt.Sprintf(`{"type":"response.created","padding":%q}`, strings.Repeat("a", int(openAIFirstOutputStageMaxBytes))))
				require.Error(t, err)
				require.Empty(t, e.pendingFrames)
				return
			}
			fragments := []string{"to=functions.", "exec code:\n", `{"cmd":"pwd"}`}
			if control == "real_exec" {
				fragments = fragments[:2]
			}
			for i, fragment := range fragments {
				payload := fmt.Sprintf(`{"type":"response.output_text.delta","delta":%q}`, fragment)
				frames, err = deliver("response.output_text.delta", payload)
				if control == "ordinary" {
					require.NoError(t, err)
					require.Len(t, frames, 1, "postcommit output never replays")
				} else if i < 2 {
					require.NoError(t, err)
					require.Empty(t, frames)
				} else {
					require.Error(t, err)
					require.Empty(t, frames)
				}
			}
			if control == "real_exec" {
				frames, err = deliver("response.completed", `{"type":"response.completed","response":{"status":"completed","output":[{"type":"custom_tool_call","name":"exec","input":"pwd"}]}}`)
				require.NoError(t, err)
				require.Len(t, frames, 4)
			}
			result := &OpenAIForwardResult{}
			e.apply(result, false, control == "cancelled")
			if control == "real_exec" {
				require.True(t, result.ExecCallObserved)
				require.False(t, result.PrecommitExecProtocolLeak)
			} else if control == "cancelled" || control == "ordinary" {
				require.False(t, result.PrecommitExecProtocolLeak)
			} else {
				require.True(t, result.PrecommitExecProtocolLeak)
				require.False(t, result.SucceededForScheduling())
			}
		})
	}
}
