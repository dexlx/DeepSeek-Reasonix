package jobs

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/event"
)

func TestCompletedJobPersistsOutputAndReleasesMemory(t *testing.T) {
	sessionPath := filepath.Join(t.TempDir(), "session.jsonl")
	m := NewManager(event.Discard)
	defer m.Close()
	m.SetActiveSessionPath("session", sessionPath)

	j := m.StartForSession("session", "bash", "persist", func(_ context.Context, out io.Writer) (string, error) {
		_, _ = io.WriteString(out, strings.Repeat("x", defaultTailBytes+1024))
		return "", nil
	})
	<-j.done

	j.mu.Lock()
	tailLen := len(j.tail)
	result := j.result
	artifactPath := j.artifactPath
	j.mu.Unlock()

	if tailLen != 0 {
		t.Fatalf("completed artifact-backed job kept %d tail bytes, want 0", tailLen)
	}
	if result != "" {
		t.Fatalf("completed artifact-backed job kept result %q, want empty", result)
	}
	if artifactPath == "" {
		t.Fatal("artifact path should be set")
	}

	res := m.WaitForSession(context.Background(), "session", []string{j.ID}, 1)
	if len(res) != 1 || len(res[0].Output) != defaultTailBytes+1024 {
		t.Fatalf("wait output len = %d, want %d", len(res[0].Output), defaultTailBytes+1024)
	}
}

func TestRestoreSessionArtifactsAndAdvanceSequence(t *testing.T) {
	sessionPath := filepath.Join(t.TempDir(), "session.jsonl")
	first := NewManager(event.Discard)
	first.SetActiveSessionPath("session", sessionPath)
	j := first.StartForSession("session", "task", "answer", func(context.Context, io.Writer) (string, error) {
		return "persisted answer", nil
	})
	<-j.done
	first.Close()

	second := NewManager(event.Discard)
	defer second.Close()
	second.SetActiveSessionPath("session", sessionPath)

	res := second.WaitForSession(context.Background(), "session", []string{j.ID}, 1)
	if len(res) != 1 || !strings.Contains(res[0].Output, "persisted answer") {
		t.Fatalf("restored wait = %+v, want persisted answer", res)
	}
	if got := second.WaitForSession(context.Background(), "session", nil, 1); len(got) != 0 {
		t.Fatalf("wait without ids should ignore restored completed artifacts, got %+v", got)
	}

	next := second.StartForSession("session", "bash", "next", func(context.Context, io.Writer) (string, error) {
		return "", nil
	})
	<-next.done
	if next.ID == j.ID {
		t.Fatalf("new job reused restored id %q", next.ID)
	}
}

func TestFinishDestroySessionPurgesOwnedJobs(t *testing.T) {
	m := NewManager(event.Discard)
	defer m.Close()

	j := m.StartForSession("session", "task", "done", func(context.Context, io.Writer) (string, error) {
		return "answer", nil
	})
	<-j.done

	done := m.DestroySession("session")
	if len(done) != 0 {
		t.Fatalf("finished job should not need destroy wait, got %d handles", len(done))
	}
	m.FinishDestroySession("session")

	if _, _, ok := m.OutputForSession("session", j.ID); ok {
		t.Fatalf("destroyed session job %s should be purged", j.ID)
	}
}

func TestSetActiveSessionPathMigratesRunningJobArtifacts(t *testing.T) {
	sessionPath := filepath.Join(t.TempDir(), "session.jsonl")
	m := NewManager(event.Discard)
	defer m.Close()

	wroteBefore := make(chan struct{})
	release := make(chan struct{})
	j := m.StartForSession("session", "bash", "migrate", func(_ context.Context, out io.Writer) (string, error) {
		_, _ = io.WriteString(out, "before\n")
		close(wroteBefore)
		<-release
		_, _ = io.WriteString(out, "after\n")
		return "", nil
	})
	<-wroteBefore

	m.SetActiveSessionPath("session", sessionPath)
	j.mu.Lock()
	gotPath := j.artifactPath
	j.mu.Unlock()
	if !strings.HasPrefix(gotPath, ArtifactDir(sessionPath)+string(filepath.Separator)) {
		t.Fatalf("artifact path = %q, want under %q", gotPath, ArtifactDir(sessionPath))
	}

	close(release)
	<-j.done
	res := m.WaitForSession(context.Background(), "session", []string{j.ID}, 1)
	if len(res) != 1 || !strings.Contains(res[0].Output, "before\n") || !strings.Contains(res[0].Output, "after\n") {
		t.Fatalf("wait after migration = %+v, want before and after output", res)
	}
}

func TestSetActiveSessionPathReportsMigrationFailure(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(ArtifactDir(sessionPath), []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	var notices []string
	m := NewManager(event.FuncSink(func(e event.Event) {
		if e.Kind == event.Notice {
			notices = append(notices, e.Text)
		}
	}))
	defer m.Close()

	j := m.StartForSession("session", "task", "migrate fail", func(context.Context, io.Writer) (string, error) {
		return "answer", nil
	})
	m.SetActiveSessionPath("session", sessionPath)
	<-j.done

	res := m.WaitForSession(context.Background(), "session", []string{j.ID}, 1)
	if len(res) != 1 || !strings.Contains(res[0].Output, "job artifact incomplete: migration:") {
		t.Fatalf("wait after migration failure = %+v, want artifact error", res)
	}
	found := false
	for _, notice := range notices {
		if strings.Contains(notice, "job artifact migration failed") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("migration failure notice not emitted, got %q", notices)
	}
}

func TestOutputReadsArtifactFromOffset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bash-1.log")
	prefix := strings.Repeat("x", 2*defaultTailBytes)
	suffix := "new output\n"
	if err := os.WriteFile(path, []byte(prefix+suffix), 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewManager(event.Discard)
	defer m.Close()
	j := &Job{
		ID:           "bash-1",
		Kind:         "bash",
		SessionID:    "session",
		status:       Running,
		readOffset:   int64(len(prefix)),
		artifactPath: path,
		done:         make(chan struct{}),
	}
	m.jobs[j.ID] = j
	m.order = append(m.order, j.ID)

	text, status, ok := m.OutputForSession("session", j.ID)
	if !ok || status != Running {
		t.Fatalf("OutputForSession ok/status = %v/%s, want true/running", ok, status)
	}
	if text != suffix {
		t.Fatalf("OutputForSession text = %q, want %q", text, suffix)
	}
	if j.readOffset != int64(len(prefix)+len(suffix)) {
		t.Fatalf("readOffset = %d, want %d", j.readOffset, len(prefix)+len(suffix))
	}
}
