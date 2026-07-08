package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/store"
)

func TestRedactSessionsScrubsHistoricalSessionArtifacts(t *testing.T) {
	dir := t.TempDir()
	const secret = "sk-real-secret-value-123456"
	sessionPath := filepath.Join(dir, "abc.jsonl")
	files := map[string]string{
		sessionPath:                                                     `{"role":"tool","content":"DEEPSEEK_API_KEY=` + secret + `"}` + "\n",
		store.SessionEventLog(sessionPath):                              `{"schema_version":1,"type":"replace","messages":[{"role":"tool","content":"DEEPSEEK_API_KEY=` + secret + `"}]}` + "\n",
		store.SessionMeta(sessionPath):                                  `{"id":"abc","preview":"DEEPSEEK_API_KEY=` + secret + `"}` + "\n",
		store.SessionGoalState(sessionPath):                             `{"goal":"rotate token ` + secret + `"}` + "\n",
		filepath.Join(store.SessionJobsDir(sessionPath), "bash-1.log"):  "DEEPSEEK_API_KEY=" + secret + "\n",
		filepath.Join(store.SessionJobsDir(sessionPath), "bash-1.json"): `{"label":"echo DEEPSEEK_API_KEY=` + secret + `"}` + "\n",
		store.SessionEventIndex(sessionPath):                            `{"schema_version":1}` + "\n",
	}
	for path, body := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	res := RedactSessions(RedactSessionsOptions{Dirs: []string{dir}})
	if len(res.Errors) > 0 {
		t.Fatalf("RedactSessions errors = %v", res.Errors)
	}
	if res.FilesChanged != 6 {
		t.Fatalf("FilesChanged = %d, want 6", res.FilesChanged)
	}
	for path := range files {
		if path == store.SessionEventIndex(sessionPath) {
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("event index should be removed after transcript rewrite, stat err=%v", err)
			}
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if strings.Contains(string(data), secret) {
			t.Fatalf("%s still leaked secret:\n%s", path, data)
		}
	}
}

func TestRedactSessionsDryRunDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	const secret = "sk-real-secret-value-123456"
	path := filepath.Join(dir, "abc.jsonl")
	body := `{"role":"tool","content":"DEEPSEEK_API_KEY=` + secret + `"}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	res := RedactSessions(RedactSessionsOptions{Dirs: []string{dir}, DryRun: true})
	if res.FilesChanged != 1 {
		t.Fatalf("FilesChanged = %d, want 1", res.FilesChanged)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != body {
		t.Fatalf("dry-run modified file:\n%s", data)
	}
}

func TestRedactSessionsSkipsLeasedSession(t *testing.T) {
	dir := t.TempDir()
	const secret = "sk-real-secret-value-123456"
	path := filepath.Join(dir, "abc.jsonl")
	if err := os.WriteFile(path, []byte(`{"role":"tool","content":"DEEPSEEK_API_KEY=`+secret+`"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lease, err := agent.TryAcquireSessionLease(path)
	if err != nil {
		t.Fatalf("TryAcquireSessionLease: %v", err)
	}
	defer lease.Release()

	res := RedactSessions(RedactSessionsOptions{Dirs: []string{dir}})
	if res.FilesSkipped != 1 {
		t.Fatalf("FilesSkipped = %d, want 1", res.FilesSkipped)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), secret) {
		t.Fatalf("leased session should not be rewritten:\n%s", data)
	}
}
