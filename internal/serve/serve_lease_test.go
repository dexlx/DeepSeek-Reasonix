package serve

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/provider"
)

func saveServeTestSession(t *testing.T, path string) {
	t.Helper()
	s := agent.NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "hi"})
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
}

// TestResumeRefusedWhenSessionLeaseHeld proves POST /resume refuses to bind a
// session another runtime holds, keeps the server on its current session, and
// reports the shared holder wording.
func TestResumeRefusedWhenSessionLeaseHeld(t *testing.T) {
	dir := t.TempDir()
	active := filepath.Join(dir, "active.jsonl")
	held := filepath.Join(dir, "held.jsonl")
	saveServeTestSession(t, active)
	saveServeTestSession(t, held)

	holder, err := agent.TryAcquireSessionLease(held)
	if err != nil {
		t.Fatalf("test holder acquire: %v", err)
	}
	defer holder.Release()

	bc := NewBroadcaster()
	ctrl := control.New(control.Options{Sink: bc, SessionDir: dir, SessionPath: active})
	server := New(ctrl, bc, config.ServeConfig{})
	leases := control.NewSessionLeaseKeeper()
	defer leases.Release()
	if err := leases.Rebind(active); err != nil {
		t.Fatalf("seed lease on active: %v", err)
	}
	server.SetSessionLeases(leases)
	srv := httptest.NewServer(server.Handler())
	defer srv.Close()

	body, err := json.Marshal(map[string]string{"path": held})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(srv.URL+"/resume", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	respBody, _ := readAll(resp)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("held resume status = %d, want 409 (body %q)", resp.StatusCode, respBody)
	}
	if !strings.Contains(respBody, "in use by another Reasonix") {
		t.Fatalf("held resume body = %q, want holder wording", respBody)
	}
	if strings.Contains(respBody, held) {
		t.Fatalf("held resume body leaks the session path: %q", respBody)
	}
	if got := filepath.Clean(ctrl.SessionPath()); got != filepath.Clean(active) {
		t.Fatalf("session path after refused resume = %q, want active %q", got, active)
	}
	if got, want := leases.HeldPath(), agent.CanonicalSessionPath(active); got != want {
		t.Fatalf("lease after refused resume = %q, want %q", got, want)
	}
}

// TestResumeMovesSessionLease proves a successful POST /resume releases the old
// session's lease and holds the new one.
func TestResumeMovesSessionLease(t *testing.T) {
	dir := t.TempDir()
	active := filepath.Join(dir, "active.jsonl")
	next := filepath.Join(dir, "next.jsonl")
	saveServeTestSession(t, active)
	saveServeTestSession(t, next)

	bc := NewBroadcaster()
	ctrl := control.New(control.Options{Sink: bc, SessionDir: dir, SessionPath: active})
	server := New(ctrl, bc, config.ServeConfig{})
	leases := control.NewSessionLeaseKeeper()
	defer leases.Release()
	if err := leases.Rebind(active); err != nil {
		t.Fatalf("seed lease on active: %v", err)
	}
	server.SetSessionLeases(leases)
	srv := httptest.NewServer(server.Handler())
	defer srv.Close()

	body, err := json.Marshal(map[string]string{"path": next})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(srv.URL+"/resume", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	respBody, _ := readAll(resp)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("resume status = %d, want 204 (body %q)", resp.StatusCode, respBody)
	}
	want, err := filepath.EvalSymlinks(next)
	if err != nil {
		t.Fatal(err)
	}
	if got, wantHeld := leases.HeldPath(), agent.CanonicalSessionPath(want); got != wantHeld {
		t.Fatalf("lease after resume = %q, want %q", got, wantHeld)
	}
	lease, err := agent.TryAcquireSessionLease(active)
	if err != nil {
		t.Fatalf("old session lease not released by resume: %v", err)
	}
	lease.Release()
}

func readAll(resp *http.Response) (string, error) {
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return strings.TrimSpace(string(b)), err
}
