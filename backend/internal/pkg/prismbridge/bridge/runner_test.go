package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/prismbridge/creds"
	"github.com/Wei-Shaw/sub2api/internal/pkg/prismbridge/prism"
)

type fakeProtocol struct {
	Protocol
	calls     []string
	start     *prism.StartResponse
	status    *prism.StatusResponse
	cancel    context.CancelFunc
	stopped   bool
	stopState string
	starts    int
}

func (f *fakeProtocol) CreateProject(context.Context, prism.Principal, map[string]any) (*prism.Project, error) {
	f.calls = append(f.calls, "project")
	return &prism.Project{ID: "p"}, nil
}
func (f *fakeProtocol) AcquireSandbox(context.Context, prism.Principal) (*prism.Sandbox, error) {
	f.calls = append(f.calls, "sandbox")
	return &prism.Sandbox{URL: "https://prism.openai.com/s/sandboxes/proxy", Token: "token", SessionID: "s"}, nil
}
func (f *fakeProtocol) AcquireResourceToken(context.Context, prism.Principal, string, string, string) (*prism.ResourceToken, error) {
	f.calls = append(f.calls, "resource")
	return &prism.ResourceToken{}, nil
}
func (f *fakeProtocol) DeliverResourceToken(context.Context, prism.Principal, *prism.Sandbox, *prism.ResourceToken, string) error {
	f.calls = append(f.calls, "deliver_resource")
	return nil
}
func (f *fakeProtocol) AcquireYSweetToken(context.Context, prism.Principal, string) (*prism.YSweetToken, error) {
	f.calls = append(f.calls, "ysweet")
	return &prism.YSweetToken{}, nil
}
func (f *fakeProtocol) DeliverYSweetToken(context.Context, prism.Principal, *prism.Sandbox, *prism.YSweetToken) error {
	f.calls = append(f.calls, "deliver_ysweet")
	return nil
}
func (f *fakeProtocol) WaitSandboxReady(context.Context, prism.Principal, *prism.Sandbox, time.Duration) (*prism.SandboxSyncStatus, bool) {
	f.calls = append(f.calls, "ready")
	return nil, true
}
func (f *fakeProtocol) ServerAction(context.Context, prism.Principal, string, ...any) (json.RawMessage, error) {
	f.calls = append(f.calls, "conversation")
	return json.RawMessage(`"cdx_one"`), nil
}
func (f *fakeProtocol) StartResponse(context.Context, prism.Principal, *prism.StartRequest) (*prism.StartResponse, error) {
	f.calls = append(f.calls, "start")
	f.starts++
	return f.start, nil
}
func (f *fakeProtocol) PollResponse(_ context.Context, _ prism.Principal, r *prism.StatusRequest, _ string) (*prism.StatusResponse, error) {
	f.calls = append(f.calls, "poll")
	if string(r.TurnState) != `{"opaque":"one"}` {
		return nil, errors.New("lost opaque state")
	}
	if f.cancel != nil {
		f.cancel()
	}
	return f.status, nil
}
func (f *fakeProtocol) StopResponse(ctx context.Context, _ prism.Principal, _, _ string, state json.RawMessage) error {
	f.stopped = ctx.Err() == nil
	f.stopState = string(state)
	return nil
}
func testPrincipal() prism.Principal { return prism.Principal{Cred: &creds.Credential{UserID: "u"}} }
func TestRunnerLifecycleBusinessFailureNoReplay(t *testing.T) {
	f := &fakeProtocol{start: &prism.StartResponse{RequestID: "r", TurnState: json.RawMessage(`{"opaque":"one"}`)}, status: &prism.StatusResponse{Done: true, Fail: true, Error: "business failure"}}
	_, err := (Runner{Client: f, PollInterval: time.Millisecond}).Run(context.Background(), testPrincipal(), []prism.InputItem{prism.NewUserItem("hi")}, "m", "")
	if err == nil || f.starts != 1 {
		t.Fatalf("err=%v starts=%d", err, f.starts)
	}
	want := []string{"project", "sandbox", "resource", "deliver_resource", "ysweet", "deliver_ysweet", "ready", "conversation", "start", "poll"}
	if len(f.calls) != len(want) {
		t.Fatal(f.calls)
	}
	for i := range want {
		if f.calls[i] != want[i] {
			t.Fatal(f.calls)
		}
	}
}
func TestRunnerCancellationStopsLatestState(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := &fakeProtocol{cancel: cancel, start: &prism.StartResponse{RequestID: "r", TurnState: json.RawMessage(`{"opaque":"one"}`)}, status: &prism.StatusResponse{TurnState: json.RawMessage(`{"opaque":"two"}`)}}
	_, err := (Runner{Client: f, PollInterval: time.Millisecond}).Run(ctx, testPrincipal(), nil, "m", "")
	if !errors.Is(err, context.Canceled) || !f.stopped || f.stopState != `{"opaque":"two"}` || f.starts != 1 {
		t.Fatalf("err=%v stopped=%v state=%s", err, f.stopped, f.stopState)
	}
}
func TestRunnerImmediateCompletedUsage(t *testing.T) {
	f := &fakeProtocol{start: &prism.StartResponse{Initial: &prism.StatusResponse{Done: true, Text: "ok", Usage: &prism.Usage{InputTokens: 3, OutputTokens: 1}}}}
	out, err := (Runner{Client: f}).Run(context.Background(), testPrincipal(), nil, "m", "")
	if err != nil || out.Text != "ok" || out.Usage.InputTokens != 3 || f.stopped {
		t.Fatalf("out=%+v err=%v", out, err)
	}
}

func TestRunnerObserverFailureStopsWithoutReplay(t *testing.T) {
	f := &fakeProtocol{start: &prism.StartResponse{RequestID: "r", TurnState: json.RawMessage(`{"opaque":"one"}`), Initial: &prism.StatusResponse{Text: "partial"}}}
	want := errors.New("downstream gone")
	_, err := (Runner{Client: f, Observe: func(st *prism.StatusResponse) error {
		if st.Text != "partial" {
			t.Fatal(st)
		}
		return want
	}}).Run(context.Background(), testPrincipal(), nil, "m", "")
	if !errors.Is(err, want) || !f.stopped || f.starts != 1 {
		t.Fatalf("err=%v stopped=%v starts=%d", err, f.stopped, f.starts)
	}
}

func TestRunnerObserverSeesTerminalLastToken(t *testing.T) {
	f := &fakeProtocol{start: &prism.StartResponse{RequestID: "r", TurnState: json.RawMessage(`{"opaque":"one"}`), Initial: &prism.StatusResponse{Text: "first"}}, status: &prism.StatusResponse{Done: true, Text: "first last"}}
	var texts []string
	_, err := (Runner{Client: f, PollInterval: time.Millisecond, Observe: func(st *prism.StatusResponse) error { texts = append(texts, st.Text); return nil }}).Run(context.Background(), testPrincipal(), nil, "m", "")
	if err != nil || len(texts) != 2 || texts[1] != "first last" || f.stopped {
		t.Fatalf("%v %v", texts, err)
	}
}
