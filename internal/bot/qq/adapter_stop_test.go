package qq

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

// Guards the Stop drain contract: the gateway loop blocks in websocket reads
// that do not honor ctx, so Stop must close the tracked connection to unblock
// them and must wait for the loop goroutine to exit before returning.
func TestStopClosesTrackedConnAndWaitsForLoop(t *testing.T) {
	srv := httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
		_, _ = io.Copy(io.Discard, ws) // hold the connection open, send nothing
	}))
	defer srv.Close()

	conn, err := websocket.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), "", srv.URL)
	if err != nil {
		t.Fatalf("dial test server: %v", err)
	}

	a := &adapter{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	tracked := make(chan struct{})
	decodeReturned := make(chan struct{})
	a.loopWG.Add(1)
	go func() {
		defer a.loopWG.Done()
		if !a.trackConn(ctx, conn) {
			conn.Close()
			return
		}
		defer a.dropConn(conn)
		close(tracked)
		var payload gatewayPayload
		_ = json.NewDecoder(conn).Decode(&payload) // blocks like connectGateway's reads
		close(decodeReturned)
	}()
	select {
	case <-tracked:
	case <-time.After(time.Second):
		t.Fatal("gateway loop did not track its connection")
	}

	done := make(chan struct{})
	go func() {
		_ = a.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not close the gateway connection and wait for the loop")
	}
	select {
	case <-decodeReturned:
	case <-time.After(time.Second):
		t.Fatal("Stop returned before the blocking gateway read exited")
	}
}

// A connection that finishes dialing after Stop must not be published: Stop
// has already emptied the tracked slot, so a late publication would leave a
// conn (and its blocked reader) that nothing can ever close.
func TestTrackConnRefusesPublicationAfterCancel(t *testing.T) {
	a := &adapter{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if a.trackConn(ctx, &websocket.Conn{}) {
		t.Fatal("trackConn published a connection after cancellation")
	}
	a.connMu.Lock()
	defer a.connMu.Unlock()
	if a.conn != nil {
		t.Fatal("cancelled publication still stored the connection")
	}
}

func TestStopWithoutStartIsSafe(t *testing.T) {
	a := &adapter{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	done := make(chan struct{})
	go func() {
		_ = a.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Stop blocked on a never-started adapter")
	}
}
