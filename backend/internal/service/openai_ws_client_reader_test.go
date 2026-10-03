package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

func TestR8SharedWSReaderPreservesFrameAndDetectsDisconnect(t *testing.T) {
	ready := make(chan *OpenAIWSClientReader, 1)
	disconnected := make(chan error, 1)
	stop := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			return
		}
		reader := NewOpenAIWSClientReader(conn, func(err error) { disconnected <- err })
		defer reader.Close()
		ready <- reader
		<-stop
	}))
	defer server.Close()
	defer close(stop)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	conn, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	defer conn.CloseNow()
	reader := <-ready
	for _, frame := range []string{"first-attempt", "next-turn"} {
		require.NoError(t, conn.Write(ctx, coderws.MessageText, []byte(frame)))
		got := reader.read()
		require.NoError(t, got.err)
		require.Equal(t, frame, string(got.payload))
	}
	_ = conn.CloseNow()
	select {
	case err := <-disconnected:
		require.Error(t, err)
	case <-ctx.Done():
		t.Fatal("active-turn disconnect was not observed")
	}
}
