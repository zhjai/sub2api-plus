//go:build unit

package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestR12SocketTerminalSettlementBeforeImmediateNextTurn(t *testing.T) {
	for _, output := range []string{r12TerminalText, r12TerminalTool} {
		for _, event := range []string{"response.incomplete", "response.done"} {
			t.Run(event+"/"+output, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				upstream := newStagedPassthroughConn()
				account := passthroughLifecycleAccount()
				account.Extra["openai_opaque_upstream"] = true
				svc := newPassthroughLifecycleService(passthroughLifecycleConfig(), upstream)
				results := make(chan *OpenAIForwardResult, 4)
				releaseFirst := make(chan struct{})
				var releaseOnce sync.Once
				release := func() { releaseOnce.Do(func() { close(releaseFirst) }) }
				server, serverErr := startPassthroughLifecycleServerWithHooks(t, ctx, svc, account, func(c *gin.Context) *OpenAIWSIngressHooks {
					_, err := svc.PrepareOpenAIOpaqueRouteEpochAttempt(ctx, c, 0, "r12", "gpt-5.1", "high", account, "")
					require.NoError(t, err)
					return &OpenAIWSIngressHooks{AfterTurn: func(turn int, result *OpenAIForwardResult, err error) {
						require.NoError(t, err)
						results <- result
						if turn == 1 {
							<-releaseFirst
						}
					}}
				})
				defer server.Close()
				defer release()
				client := dialPassthroughLifecycleClientWithPayload(t, server, `{"type":"response.create","model":"gpt-5.1","reasoning":{"effort":"high"},"tools":[{"type":"custom","name":"exec"}]}`)
				defer client.CloseNow()
				requirePassthroughUpstreamWrite(t, upstream, time.Second)
				terminal := r12Terminal(event, "incomplete", "stream_terminated", output)
				upstream.Send(string(terminal))
				frame, err := readPassthroughLifecycleFrame(t, client, time.Second)
				require.NoError(t, err)
				require.JSONEq(t, string(terminal), string(frame))
				writeCtx, cancelWrite := context.WithTimeout(ctx, time.Second)
				err = client.Write(writeCtx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5.2","reasoning":{"effort":"low"}}`))
				cancelWrite()
				require.NoError(t, err)
				var first *OpenAIForwardResult
				select {
				case first = <-results:
				case <-time.After(time.Second):
					t.Fatal("delivered terminal was not settled")
				}
				require.True(t, first.RequiresSessionAccountEscape())
				require.True(t, first.ResponsesMeaningfulOutput)
				require.Equal(t, output == r12TerminalTool, first.ResponsesToolCallForwarded)
				require.Equal(t, "gpt-5.1", first.Model)
				require.Equal(t, "high", *first.RequestedReasoningEffort)
				require.Equal(t, 7, first.Usage.InputTokens)
				select {
				case frame := <-upstream.writes:
					t.Fatalf("next request overtook settlement: %s", frame)
				case <-time.After(30 * time.Millisecond):
				}
				release()
				next := requirePassthroughUpstreamWrite(t, upstream, time.Second)
				require.Equal(t, "gpt-5.2", gjson.GetBytes(next, "model").String())
				upstream.Send(`{"type":"response.completed","response":{"id":"resp_second","model":"gpt-5.2","status":"completed","usage":{"input_tokens":11,"output_tokens":2}}}`)
				_, err = readPassthroughLifecycleFrame(t, client, time.Second)
				require.NoError(t, err)
				// Closing immediately must not lose response ownership or duplicate billing.
				require.NoError(t, client.Close(coderws.StatusNormalClosure, "done"))
				select {
				case err := <-serverErr:
					require.NoError(t, err)
				case <-time.After(3 * time.Second):
					t.Fatal("relay did not close")
				}
				select {
				case second := <-results:
					require.Equal(t, "gpt-5.2", second.Model)
					require.Equal(t, "low", *second.RequestedReasoningEffort)
					require.Equal(t, 11, second.Usage.InputTokens)
					require.False(t, second.RequiresSessionAccountEscape())
				default:
					t.Fatal("missing second turn billing")
				}
				require.Empty(t, results)
				for _, id := range []string{"resp_r12", "resp_second"} {
					owner, err := svc.getOpenAIWSStateStore().GetResponseAccount(ctx, 0, id)
					require.NoError(t, err)
					require.Equal(t, account.ID, owner)
					epoch, found, err := svc.getOpenAIWSStateStore().GetResponseRouteEpoch(ctx, 0, id, account.ID)
					require.NoError(t, err)
					require.True(t, found)
					require.Zero(t, epoch)
				}
			})
		}
	}
}

func TestR12SocketBinaryDeliveryDoesNotReplay(t *testing.T) {
	for _, staged := range []bool{false, true} {
		t.Run(fmt.Sprint(staged), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			upstream := newStagedPassthroughConn()
			results := make(chan *OpenAIForwardResult, 4)
			svc := newPassthroughLifecycleService(passthroughLifecycleConfig(), upstream)
			server, serverErr := startPassthroughLifecycleServerWithHooks(t, ctx, svc, passthroughLifecycleAccount(), func(*gin.Context) *OpenAIWSIngressHooks {
				return &OpenAIWSIngressHooks{AfterTurn: func(_ int, result *OpenAIForwardResult, _ error) { results <- result }}
			})
			defer server.Close()
			client := dialPassthroughLifecycleClientWithPayload(t, server, `{"type":"response.create","model":"gpt-5.1","tools":[{"type":"custom","name":"exec"}]}`)
			defer client.CloseNow()
			requirePassthroughUpstreamWrite(t, upstream, time.Second)
			if staged {
				upstream.Send(`{"type":"response.created","response":{"id":"resp_r12"}}`)
				upstream.Send(`{"type":"response.output_text.delta","delta":"to=functions."}`)
			}
			upstream.Send(`{"type":"keepalive"}`)
			frame, err := readPassthroughLifecycleFrame(t, client, time.Second)
			require.NoError(t, err)
			require.Equal(t, "keepalive", gjson.GetBytes(frame, "type").String(), "keepalive cannot release staged text")
			upstream.frames <- stagedPassthroughFrame{messageType: coderws.MessageBinary, payload: []byte{0, 1, 2}}
			if staged {
				for _, event := range []string{"response.created", "response.output_text.delta"} {
					readCtx, cancelRead := context.WithTimeout(ctx, time.Second)
					typ, frame, err := client.Read(readCtx)
					cancelRead()
					require.NoError(t, err)
					require.Equal(t, coderws.MessageText, typ)
					require.Equal(t, event, gjson.GetBytes(frame, "type").String())
				}
			}
			readCtx, cancelRead := context.WithTimeout(ctx, time.Second)
			typ, binary, err := client.Read(readCtx)
			cancelRead()
			require.NoError(t, err)
			require.Equal(t, coderws.MessageBinary, typ)
			require.Equal(t, []byte{0, 1, 2}, binary)
			leak := `{"type":"response.output_text.delta","delta":"to=functions.exec code:\n{\"cmd\":\"pwd\"}"}`
			if staged {
				leak = `{"type":"response.output_text.delta","delta":"exec code:\n{\"cmd\":\"pwd\"}"}`
			}
			upstream.Send(leak)
			frame, err = readPassthroughLifecycleFrame(t, client, time.Second)
			require.NoError(t, err)
			require.JSONEq(t, leak, string(frame))
			upstream.Send(string(r12Terminal("response.completed", "completed", "", "[]")))
			_, err = readPassthroughLifecycleFrame(t, client, time.Second)
			require.NoError(t, err)
			var result *OpenAIForwardResult
			select {
			case result = <-results:
			case <-time.After(time.Second):
				t.Fatal("missing completed turn")
			}
			require.True(t, result.ToolCapabilityFailure)
			require.False(t, result.PrecommitExecProtocolLeak)
			require.True(t, result.RequiresSessionAccountEscape())
			require.NoError(t, client.Close(coderws.StatusNormalClosure, "done"))
			select {
			case err := <-serverErr:
				var failover *UpstreamFailoverError
				require.False(t, errors.As(err, &failover))
				require.NoError(t, err)
			case <-time.After(3 * time.Second):
				t.Fatal("relay did not close")
			}
			require.Empty(t, upstream.writes, "the initial request cannot replay after binary output")
			require.Empty(t, results, "completed turn must be billed once")
		})
	}
}

func TestR12SocketDisconnectedTurnRetainsTerminalUsage(t *testing.T) {
	for _, event := range []string{"response.incomplete", "error", "paired_error"} {
		t.Run(event, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			upstream := newStagedPassthroughConn()
			results := make(chan *OpenAIForwardResult, 4)
			svc := newPassthroughLifecycleService(passthroughLifecycleConfig(), upstream)
			account := passthroughLifecycleAccount()
			server, serverErr := startPassthroughLifecycleServerWithHooks(t, ctx, svc, account, func(*gin.Context) *OpenAIWSIngressHooks {
				return &OpenAIWSIngressHooks{AfterTurn: func(_ int, result *OpenAIForwardResult, err error) {
					require.NoError(t, err, "upstream usage must remain billable after downstream closes")
					results <- result
				}}
			})
			defer server.Close()
			client := dialPassthroughLifecycleClient(t, server)
			defer client.CloseNow()
			requirePassthroughUpstreamWrite(t, upstream, time.Second)
			upstream.Send(`{"type":"response.created","response":{"id":"resp_r12","model":"gpt-5.1"}}`)
			_, err := readPassthroughLifecycleFrame(t, client, time.Second)
			require.NoError(t, err)
			require.NoError(t, client.Close(coderws.StatusNormalClosure, "cancelled"))
			if event == "paired_error" {
				upstream.Send(`{"type":"error","error":{"code":"invalid_request_error","message":"failed"},"usage":{"input_tokens":1,"output_tokens":1}}`)
				upstream.Send(string(r12Terminal("response.failed", "failed", "", r12TerminalText)))
			} else if event == "error" {
				upstream.Send(`{"type":"error","error":{"code":"invalid_request_error","message":"failed"},"usage":{"input_tokens":7,"output_tokens":3}}`)
				upstream.Fail(errors.New("upstream closed"))
			} else {
				upstream.Send(string(r12Terminal(event, "incomplete", "stream_terminated", r12TerminalText)))
			}
			select {
			case err := <-serverErr:
				require.NoError(t, err)
			case <-time.After(3 * time.Second):
				t.Fatal("relay did not finish draining usage")
			}
			select {
			case result := <-results:
				require.Equal(t, 7, result.Usage.InputTokens)
				require.Equal(t, 3, result.Usage.OutputTokens)
				require.True(t, result.ClientDisconnect)
				require.False(t, result.RequiresSessionAccountEscape())
				require.False(t, result.ToolCapabilityFailure)
				require.False(t, result.ResponsesMeaningfulOutput)
			default:
				t.Fatal("terminal usage was lost after disconnect")
			}
			require.Empty(t, results, "terminal must be billed exactly once")
			owner, err := svc.getOpenAIWSStateStore().GetResponseAccount(ctx, 0, "resp_r12")
			require.NoError(t, err)
			require.Equal(t, account.ID, owner)
		})
	}
}
