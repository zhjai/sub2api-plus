//go:build unit

package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// The selected account stays immutable; only the authoritative limit changes.
type r13LiveRPMRepo struct {
	*liveTestAccountRepo
	limit atomic.Int64
	reads atomic.Int64
}

func (r *r13LiveRPMRepo) GetAccountRPMLimit(_ context.Context, id int64) (int, error) {
	if id != r.account.ID {
		return 0, errors.New("unexpected Live account owner")
	}
	r.reads.Add(1)
	return int(r.limit.Load()), nil
}

type r13LiveRPMStore struct {
	*liveTestStore
	rpmMu            sync.Mutex
	used, attempts   int
	failed           bool
	suppressObserver bool
	observerStopped  chan struct{}
}

func (s *r13LiveRPMStore) AdmitAccountRPM(_ context.Context, id int64, limit int, _ string) (AccountRPMDecision, error) {
	s.rpmMu.Lock()
	defer s.rpmMu.Unlock()
	s.attempts++
	if id != 91 || s.failed {
		return AccountRPMDecision{}, errors.New("synthetic admission storage unavailable")
	}
	if s.used >= limit {
		return AccountRPMDecision{Used: s.used, RetryAfter: time.Minute}, nil
	}
	s.used++
	return AccountRPMDecision{Allowed: true, Used: s.used}, nil
}

func (s *r13LiveRPMStore) ReadAccountRPMBatch(context.Context, map[int64]int) (map[int64]AccountRPMDecision, error) {
	return nil, errors.New("unexpected capacity read at send boundary")
}

func (s *r13LiveRPMStore) ClaimLiveController(ctx context.Context, hash, controller, owner string) (bool, error) {
	if controller == LiveControllerObserver && s.suppressObserver {
		// End the detached recovery task deterministically after proxy denial.
		close(s.observerStopped)
		return false, nil
	}
	return s.liveTestStore.ClaimLiveController(ctx, hash, controller, owner)
}

func (s *r13LiveRPMStore) counts() (int, int) {
	s.rpmMu.Lock()
	defer s.rpmMu.Unlock()
	return s.used, s.attempts
}

type r13LiveSocketPeer struct {
	mu     sync.Mutex
	frames []liveTestFrame
	closed chan struct{}
}

func (p *r13LiveSocketPeer) snapshot() []liveTestFrame {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]liveTestFrame(nil), p.frames...)
}

func r13LiveSocketHarness(t *testing.T) (*OpenAIGatewayService, *LiveCallRecord, *r13LiveRPMRepo, *r13LiveRPMStore, *r13LiveSocketPeer) {
	t.Helper()
	peer := &r13LiveSocketPeer{closed: make(chan struct{})}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(peer.closed)
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		for {
			kind, payload, err := conn.Read(ctx)
			if err != nil {
				return
			}
			peer.mu.Lock()
			peer.frames = append(peer.frames, liveTestFrame{messageType: kind, payload: payload})
			peer.mu.Unlock()
			if gjson.GetBytes(payload, "type").String() == "session.close" {
				return
			}
			if conn.Write(ctx, kind, payload) != nil {
				return
			}
		}
	}))
	t.Cleanup(upstream.Close)
	account := &Account{ID: 91, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "synthetic", "chatgpt_account_id": "synthetic"},
		Extra:       map[string]any{"rpm_limit": 1}}
	repo := &r13LiveRPMRepo{liveTestAccountRepo: &liveTestAccountRepo{account: account}}
	repo.limit.Store(1)
	cipher := newLiveAttestationCipher(&config.Config{JWT: config.JWTConfig{Secret: "synthetic-live-r13"}})
	record := &LiveCallRecord{CallID: "call_r13", CallHash: hashLiveCallID("call_r13"), AccountID: account.ID,
		APIKeyID: 22, UserID: 33, Controller: LiveControllerPending, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(10 * time.Second)}
	var err error
	record.AttestationCiphertext, err = cipher.Encrypt(`{"v":1,"s":0,"t":"synthetic"}`)
	require.NoError(t, err)
	store := &r13LiveRPMStore{liveTestStore: &liveTestStore{}, suppressObserver: true, observerStopped: make(chan struct{})}
	require.NoError(t, store.SaveLiveCall(t.Context(), record, time.Minute))
	svc := &OpenAIGatewayService{accountRepo: repo, cache: store, liveAttestationCipher: cipher,
		openaiWSPassthroughDialer: &hardRPMLocalWSDialer{url: "ws" + strings.TrimPrefix(upstream.URL, "http")}}
	return svc, record, repo, store, peer
}

func r13LiveControls() []liveTestFrame {
	return []liveTestFrame{
		{messageType: coderws.MessageText, payload: []byte(`{"type":"session.update","session":{"modalities":["text"]}}`)},
		{messageType: coderws.MessageBinary, payload: []byte(`{"type":"input_audio_buffer.append","audio":"AA=="}`)},
		{messageType: coderws.MessageText, payload: []byte(`{"type":"conversation.item.create","item":{"type":"function_call_output","call_id":"synthetic","output":"ok"}}`)},
	}
}

// Both client and upstream are real WebSockets. Echoes are wire barriers: each
// accepted frame must arrive upstream before the next assertion or limit change.
func TestR13LiveSocketGenerationAdmission(t *testing.T) {
	for _, firstKind := range []coderws.MessageType{coderws.MessageText, coderws.MessageBinary} {
		for _, mode := range []string{"capacity", "cache_failure", "fresh_limit"} {
			t.Run(firstKind.String()+"/"+mode, func(t *testing.T) {
				svc, record, repo, store, peer := r13LiveSocketHarness(t)
				if mode == "fresh_limit" {
					repo.limit.Store(2)
				}
				result := make(chan error, 1)
				downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					conn, err := coderws.Accept(w, r, nil)
					if err != nil {
						result <- err
						return
					}
					defer conn.CloseNow()
					result <- svc.ProxyLiveSideband(r.Context(), record, conn)
				}))
				defer downstream.Close()
				ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
				defer cancel()
				client, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(downstream.URL, "http"), nil)
				require.NoError(t, err)
				defer client.CloseNow()
				echo := func(frame liveTestFrame) {
					require.NoError(t, client.Write(ctx, frame.messageType, frame.payload))
					kind, payload, err := client.Read(ctx)
					require.NoError(t, err)
					require.Equal(t, frame.messageType, kind)
					require.Equal(t, frame.payload, payload)
				}
				for _, frame := range r13LiveControls() {
					echo(frame)
				}
				used, attempts := store.counts()
				require.Zero(t, used)
				require.Zero(t, attempts)
				require.Zero(t, repo.reads.Load())
				echo(liveTestFrame{messageType: firstKind, payload: []byte(`{"type":"response.create","response":{"instructions":"synthetic first"}}`)})
				// Controls still pass when capacity is exhausted.
				for _, frame := range r13LiveControls() {
					echo(frame)
				}
				if mode == "cache_failure" {
					store.rpmMu.Lock()
					store.failed = true
					store.rpmMu.Unlock()
				}
				if mode == "fresh_limit" {
					repo.limit.Store(1)
				}
				secondKind := coderws.MessageBinary
				if firstKind == coderws.MessageBinary {
					secondKind = coderws.MessageText
				}
				require.NoError(t, client.Write(ctx, secondKind, []byte(`{"type":"response.create","response":{"instructions":"must never reach upstream"}}`)))
				_, _, err = client.Read(ctx)
				require.Equal(t, coderws.StatusTryAgainLater, coderws.CloseStatus(err))
				select {
				case err = <-result:
				case <-ctx.Done():
					t.Fatal("Live proxy did not terminate denied write")
				}
				var denied *AccountRPMError
				require.ErrorAs(t, err, &denied)
				require.True(t, denied.NoMigration)
				require.Equal(t, mode == "cache_failure", denied.Unavailable)
				require.Equal(t, record.AccountID, denied.AccountID)
				for _, done := range []<-chan struct{}{peer.closed, store.observerStopped} {
					select {
					case <-done:
					case <-ctx.Done():
						t.Fatal("Live socket/recovery task did not stop")
					}
				}
				frames := peer.snapshot()
				require.Len(t, frames, 7, "only six controls and the first generation reach the wire")
				require.Equal(t, firstKind, frames[3].messageType)
				used, attempts = store.counts()
				require.Equal(t, 1, used)
				require.Equal(t, 2, attempts)
				require.EqualValues(t, 2, repo.reads.Load(), "fresh authoritative read for each generation")
				bound, err := store.GetLiveCall(ctx, record.CallHash)
				require.NoError(t, err)
				require.Equal(t, record.AccountID, bound.AccountID)
				require.Equal(t, record.CallID, bound.CallID)
			})
		}
	}
}

// Internal observers and tool runners use the same production dial result.
// Exercise generation writes on that result, then run the actual observer until
// its expiry sends session.close; the control must work with exhausted capacity.
func TestR13LiveSocketInternalObserverWrapper(t *testing.T) {
	svc, record, repo, store, peer := r13LiveSocketHarness(t)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	conn, err := svc.dialLiveSideband(ctx, record)
	require.NoError(t, err)
	defer conn.Close()
	_, wrapped := conn.(*accountRPMLiveFrameConn)
	require.True(t, wrapped)
	for _, frame := range append(r13LiveControls(), liveTestFrame{messageType: coderws.MessageBinary, payload: []byte(`{"type":"response.create"}`)}) {
		require.NoError(t, conn.WriteFrame(ctx, frame.messageType, frame.payload))
		kind, payload, err := conn.ReadFrame(ctx)
		require.NoError(t, err)
		require.Equal(t, frame.messageType, kind)
		require.Equal(t, frame.payload, payload)
	}
	err = conn.WriteFrame(ctx, coderws.MessageText, []byte(`{"type":"response.create"}`))
	var denied *AccountRPMError
	require.ErrorAs(t, err, &denied)
	require.True(t, denied.NoMigration)
	require.Equal(t, record.AccountID, denied.AccountID)
	record.Controller = LiveControllerObserver
	record.ExpiresAt = time.Now().Add(100 * time.Millisecond)
	require.NoError(t, store.SaveLiveCall(ctx, record, time.Minute))
	require.ErrorIs(t, svc.runLiveObserverConnection(record, conn), context.DeadlineExceeded)
	select {
	case <-peer.closed:
	case <-ctx.Done():
		t.Fatal("observer session.close did not reach upstream")
	}
	frames := peer.snapshot()
	require.Len(t, frames, 5, "setup, control, tool result, one generation and observer close only")
	require.Equal(t, "session.close", gjson.GetBytes(frames[4].payload, "type").String())
	used, attempts := store.counts()
	require.Equal(t, 1, used)
	require.Equal(t, 2, attempts)
	require.EqualValues(t, 2, repo.reads.Load())
	bound, err := store.GetLiveCall(ctx, record.CallHash)
	require.NoError(t, err)
	require.Equal(t, record.AccountID, bound.AccountID)
}
