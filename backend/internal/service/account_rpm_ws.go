package service

import (
	"context"
	"strings"
	"sync/atomic"

	openaiwsv2 "github.com/Wei-Shaw/sub2api/internal/service/openai_ws_v2"
	coderws "github.com/coder/websocket"
	"github.com/tidwall/gjson"
)

// Wrap the upstream writer so the first frame and all later relay frames use
// the same admission boundary, after validation and before any upstream write.
type accountRPMFrameConn struct {
	openaiwsv2.FrameConn
	admit func(context.Context) error
	sent  atomic.Int64
}

// Every Live controller, including the internal observer, receives this wrapper
// from dialLiveSideband. The existing call always owns this account: no migration
// is safe even if its first generation frame is denied.
type accountRPMLiveFrameConn struct {
	liveFrameConn
	admit func(context.Context) error
}

func (c *accountRPMLiveFrameConn) WriteFrame(ctx context.Context, kind coderws.MessageType, payload []byte) error {
	if (kind == coderws.MessageText || kind == coderws.MessageBinary) && strings.TrimSpace(gjson.GetBytes(payload, "type").String()) == "response.create" {
		if err := c.admit(ctx); err != nil {
			return accountRPMWSTurnError(err, 2)
		}
	}
	return c.liveFrameConn.WriteFrame(ctx, kind, payload)
}

func (c *accountRPMFrameConn) WriteFrame(ctx context.Context, kind coderws.MessageType, payload []byte) error {
	if (kind == coderws.MessageText || kind == coderws.MessageBinary) && strings.TrimSpace(gjson.GetBytes(payload, "type").String()) == "response.create" {
		if err := c.admit(ctx); err != nil {
			return accountRPMWSTurnError(err, int(c.sent.Load())+1)
		}
		c.sent.Add(1)
	}
	return c.FrameConn.WriteFrame(ctx, kind, payload)
}
