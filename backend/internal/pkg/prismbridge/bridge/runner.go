// Copyright (c) oai-prism contributors. MIT; see LICENSE.
// Lifecycle port of facade/runner.go: project, sandbox, resource and Y-Sweet
// token handoff, server-action registration, start/status/stop. Admission,
// account selection and billing belong exclusively to Sub2API.
package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/prismbridge/prism"
)

type Protocol interface {
	CreateProject(context.Context, prism.Principal, map[string]any) (*prism.Project, error)
	AcquireSandbox(context.Context, prism.Principal) (*prism.Sandbox, error)
	AcquireResourceToken(context.Context, prism.Principal, string, string, string) (*prism.ResourceToken, error)
	DeliverResourceToken(context.Context, prism.Principal, *prism.Sandbox, *prism.ResourceToken, string) error
	AcquireYSweetToken(context.Context, prism.Principal, string) (*prism.YSweetToken, error)
	DeliverYSweetToken(context.Context, prism.Principal, *prism.Sandbox, *prism.YSweetToken) error
	WaitSandboxReady(context.Context, prism.Principal, *prism.Sandbox, time.Duration) (*prism.SandboxSyncStatus, bool)
	ServerAction(context.Context, prism.Principal, string, ...any) (json.RawMessage, error)
	StartResponse(context.Context, prism.Principal, *prism.StartRequest) (*prism.StartResponse, error)
	PollResponse(context.Context, prism.Principal, *prism.StatusRequest, string) (*prism.StatusResponse, error)
	StopResponse(context.Context, prism.Principal, string, string, json.RawMessage) error
}

type Runner struct {
	Client       Protocol
	PollInterval time.Duration
	ReadyTimeout time.Duration
	// Observe is synchronous and runs before terminal acceptance. Returning an
	// error stops the active inference without replaying it.
	Observe func(*prism.StatusResponse) error
}

// Run never retries inference, changes accounts, or emits speculative tool
// text. Each request has an isolated workspace and replays its complete input.
// The source project has no verified project/sandbox deletion operation; no
// guessed destructive endpoint is used. Active inference is explicitly stopped.
func (r Runner) Run(ctx context.Context, p prism.Principal, input []prism.InputItem, model, effort string) (*prism.StatusResponse, error) {
	ctx = prism.WithNoReplay(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	project, err := r.Client.CreateProject(ctx, p, nil)
	if err != nil {
		return nil, err
	}
	if project.Key() == "" {
		return nil, errors.New("Prism returned an empty project")
	}
	sb, err := r.Client.AcquireSandbox(ctx, p)
	if err != nil {
		return nil, err
	}
	if !sb.Usable() {
		return nil, errors.New("Prism sandbox unavailable")
	}
	rt, err := r.Client.AcquireResourceToken(ctx, p, project.Key(), sb.SessionID, sb.Token)
	if err != nil {
		return nil, err
	}
	if err = r.Client.DeliverResourceToken(ctx, p, sb, rt, project.Key()); err != nil {
		return nil, err
	}
	yt, err := r.Client.AcquireYSweetToken(ctx, p, project.Key())
	if err != nil {
		return nil, err
	}
	if err = r.Client.DeliverYSweetToken(ctx, p, sb, yt); err != nil {
		return nil, err
	}
	ready := r.ReadyTimeout
	if ready <= 0 {
		ready = 90 * time.Second
	}
	if _, ok := r.Client.WaitSandboxReady(ctx, p, sb, ready); !ok {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, errors.New("Prism sandbox synchronization timed out")
	}
	raw, err := r.Client.ServerAction(ctx, p, prism.ActionCreateProjectConversation, project.Key())
	if err != nil {
		return nil, err
	}
	var cid string
	if json.Unmarshal(raw, &cid) != nil || !strings.HasPrefix(cid, "cdx") {
		return nil, errors.New("Prism returned an invalid conversation ID")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	start, err := r.Client.StartResponse(ctx, p, &prism.StartRequest{Input: input, ConversationID: cid, Model: model, ReasoningEffort: effort, UserID: p.Cred.UserID, Metadata: map[string]any{"projectId": project.Key(), "frontend_origin": "https://prism.openai.com", "sandbox_url": sb.URL, "sandbox_token": sb.Token}})
	if err != nil {
		return nil, err
	}
	if start == nil {
		return nil, errors.New("Prism returned no start state")
	}
	state := start.TurnState
	requestID := start.RequestID
	finished := false
	defer func() {
		if !finished && requestID != "" {
			stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = r.Client.StopResponse(prism.WithNoReplay(stopCtx), p, requestID, cid, state)
		}
	}()
	status := start.Initial
	interval := r.PollInterval
	if interval <= 0 {
		interval = 500 * time.Millisecond
	}
	prev := ""
	for {
		if status != nil {
			if len(status.TurnState) > 0 {
				state = status.TurnState
			}
			if status.RequestID != "" {
				requestID = status.RequestID
			}
			if status.Fail {
				finished = true
				return status, errors.New("Prism upstream generation failed")
			}
			if r.Observe != nil {
				if err := r.Observe(status); err != nil {
					return status, err
				}
			}
			if status.Done {
				finished = true
				return status, nil
			}
			prev = status.Text
		}
		if requestID == "" || len(state) == 0 {
			return status, errors.New("Prism omitted inference continuation state")
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return status, ctx.Err()
		case <-timer.C:
		}
		status, err = r.Client.PollResponse(ctx, p, &prism.StatusRequest{RequestID: requestID, TurnState: state}, prev)
		if err != nil {
			return status, err
		}
	}
}
