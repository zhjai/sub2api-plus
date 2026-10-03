package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	coderws "github.com/coder/websocket"
)

type openAIWSClientReadResult struct {
	messageType coderws.MessageType
	payload     []byte
	err         error
}

var ErrOpenAIWSClientQueueOverflow = errors.New("openai websocket client input queue is full")

// One connection reader survives account attempts and observes disconnects
// while an upstream turn is running. The queue is bounded to one future frame.
type OpenAIWSClientReader struct {
	conn          *coderws.Conn
	frames        chan openAIWSClientReadResult
	done          chan struct{}
	stop          chan struct{}
	once          sync.Once
	serverClosing atomic.Bool
}

func NewOpenAIWSClientReader(conn *coderws.Conn, disconnected func(error)) *OpenAIWSClientReader {
	r := &OpenAIWSClientReader{conn: conn, frames: make(chan openAIWSClientReadResult, 1), done: make(chan struct{}), stop: make(chan struct{})}
	go func() {
		defer close(r.done)
		for {
			typ, payload, err := conn.Read(context.Background())
			if err != nil && disconnected != nil && !r.serverClosing.Load() {
				disconnected(err)
			}
			select {
			case r.frames <- openAIWSClientReadResult{messageType: typ, payload: payload, err: err}:
			case <-r.stop:
				return
			default:
				// Reject excess input instead of blocking the only disconnect reader.
				if disconnected != nil && !r.serverClosing.Load() {
					disconnected(ErrOpenAIWSClientQueueOverflow)
				}
				r.MarkServerClose()
				_ = conn.CloseNow()
				return
			}
			if err != nil {
				return
			}
		}
	}()
	return r
}

func (r *OpenAIWSClientReader) read() openAIWSClientReadResult {
	select {
	case result := <-r.frames:
		return result
	case <-r.done:
		select {
		case result := <-r.frames:
			return result
		default:
			return openAIWSClientReadResult{err: errOpenAIWSConnClosed}
		}
	}
}

func (r *OpenAIWSClientReader) Close() {
	r.once.Do(func() { r.MarkServerClose(); close(r.stop); _ = r.conn.CloseNow(); <-r.done })
}

func (r *OpenAIWSClientReader) MarkServerClose() {
	if r != nil {
		r.serverClosing.Store(true)
	}
}

// ReadOpenAIWSClientMessage keeps one reader alive while control events send
// their close frame, then closes the transport and joins that reader.
func ReadOpenAIWSClientMessage(
	controlCtx context.Context,
	conn *coderws.Conn,
	timeout time.Duration,
	timeoutStatus coderws.StatusCode,
	timeoutReason string,
) (coderws.MessageType, []byte, error) {
	return readOpenAIWSClientMessageWithTimeoutStart(
		controlCtx,
		conn,
		timeout,
		timeoutStatus,
		timeoutReason,
		nil,
		nil,
	)
}

// readOpenAIWSClientMessageWithTimeoutStart supports readers whose timeout
// starts after a state transition, such as a completed passthrough turn. When
// timeoutActive is nil, a positive timeout starts immediately.
func readOpenAIWSClientMessageWithTimeoutStart(
	controlCtx context.Context,
	conn *coderws.Conn,
	timeout time.Duration,
	timeoutStatus coderws.StatusCode,
	timeoutReason string,
	timeoutStart <-chan struct{},
	timeoutActive func() bool,
	sharedReader ...*OpenAIWSClientReader,
) (coderws.MessageType, []byte, error) {
	if conn == nil {
		return 0, nil, errors.New("openai websocket client connection is nil")
	}
	if controlCtx == nil {
		controlCtx = context.Background()
	}

	readDone := make(chan openAIWSClientReadResult, 1)
	go func() {
		if len(sharedReader) > 0 && sharedReader[0] != nil {
			readDone <- sharedReader[0].read()
			return
		}
		messageType, payload, err := conn.Read(context.Background())
		readDone <- openAIWSClientReadResult{messageType: messageType, payload: payload, err: err}
	}()

	var timer *time.Timer
	var timeoutCh <-chan time.Time
	startTimeout := func() {
		if timeout <= 0 || (timeoutActive != nil && !timeoutActive()) {
			return
		}
		if timer == nil {
			timer = time.NewTimer(timeout)
		} else {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(timeout)
		}
		timeoutCh = timer.C
	}
	if timeoutActive == nil || timeoutActive() {
		startTimeout()
	}
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()

	closeAndJoin := func(status coderws.StatusCode, reason string, cause error) (coderws.MessageType, []byte, error) {
		_ = conn.Close(status, reason)
		_ = conn.CloseNow()
		<-readDone
		return 0, nil, NewOpenAIWSClientCloseError(status, reason, cause)
	}

	for {
		select {
		case result := <-readDone:
			return result.messageType, result.payload, result.err
		case <-timeoutStart:
			startTimeout()
		case <-timeoutCh:
			return closeAndJoin(timeoutStatus, timeoutReason, context.DeadlineExceeded)
		case <-controlCtx.Done():
			cause := context.Cause(controlCtx)
			if errors.Is(cause, ErrOpenAIWSIngressLeaseLost) {
				return closeAndJoin(
					coderws.StatusTryAgainLater,
					"websocket ingress capacity lease lost; please reconnect",
					cause,
				)
			}
			return closeAndJoin(coderws.StatusGoingAway, "websocket request canceled", cause)
		}
	}
}
