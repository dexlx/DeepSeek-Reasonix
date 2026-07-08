package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"reasonix/internal/agent"
	"reasonix/internal/fileutil"
	"reasonix/internal/secrets"
	"reasonix/internal/store"
)

// RedactSessionsOptions controls historical session-log redaction.
type RedactSessionsOptions struct {
	Dirs   []string
	DryRun bool
}

// RedactSessionsResult summarizes a historical session-log redaction run.
type RedactSessionsResult struct {
	Dirs           []string `json:"dirs"`
	FilesScanned   int64    `json:"files_scanned"`
	FilesChanged   int64    `json:"files_changed"`
	FilesSkipped   int64    `json:"files_skipped"`
	BytesRewritten int64    `json:"bytes_rewritten"`
	DryRun         bool     `json:"dry_run"`
	Errors         []string `json:"errors,omitempty"`
}

// RedactSessions masks credential-shaped values already persisted in Reasonix
// session transcripts, event logs, branch metadata previews, and background-job
// artifacts. It is intentionally scoped to known Reasonix session directories;
// it is not a general-purpose filesystem scrubber.
func RedactSessions(opts RedactSessionsOptions) RedactSessionsResult {
	dirs := redactSessionDirs(opts.Dirs)
	res := RedactSessionsResult{Dirs: dirs, DryRun: opts.DryRun}
	for _, dir := range dirs {
		if err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("%s: %v", path, err))
				if d != nil && d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() || !redactSessionCandidate(path) {
				return nil
			}
			res.FilesScanned++
			if sessionPath := redactionSessionPath(path); sessionPath != "" && sessionRedactionLeaseHeld(sessionPath) {
				res.FilesSkipped++
				return nil
			}
			changed, rewritten, err := redactSessionFile(path, opts.DryRun)
			if err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("%s: %v", path, err))
				return nil
			}
			if !changed {
				return nil
			}
			res.FilesChanged++
			res.BytesRewritten += rewritten
			if !opts.DryRun {
				removeDerivedSessionIndex(path)
			}
			return nil
		}); err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("%s: %v", dir, err))
		}
	}
	return res
}

func redactSessionDirs(in []string) []string {
	var candidates []string
	if len(in) > 0 {
		candidates = append(candidates, in...)
	} else {
		candidates = append(candidates, sessionBundleSearchDirs()...)
	}
	seen := map[string]bool{}
	var out []string
	for _, dir := range candidates {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		if abs, err := filepath.Abs(dir); err == nil {
			dir = abs
		}
		dir = filepath.Clean(dir)
		if seen[dir] {
			continue
		}
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			continue
		}
		seen[dir] = true
		out = append(out, dir)
	}
	sort.Strings(out)
	return out
}

func redactSessionCandidate(path string) bool {
	name := filepath.Base(path)
	switch {
	case store.IsSessionTranscriptName(name):
		return true
	case strings.HasSuffix(name, ".jsonl.meta"):
		return true
	case strings.HasSuffix(name, ".events.jsonl"):
		return true
	case strings.HasSuffix(name, ".guardian.jsonl"):
		return true
	case strings.HasSuffix(name, ".goal-state.json"):
		return true
	case filepath.Base(filepath.Dir(path)) != "" && strings.HasSuffix(filepath.Base(filepath.Dir(path)), ".jobs"):
		return strings.HasSuffix(name, ".log") || strings.HasSuffix(name, ".json")
	default:
		return false
	}
}

func redactionSessionPath(path string) string {
	name := filepath.Base(path)
	switch {
	case store.IsSessionTranscriptName(name), strings.HasSuffix(name, ".guardian.jsonl"):
		return path
	case strings.HasSuffix(path, ".jsonl.meta"):
		return strings.TrimSuffix(path, ".meta")
	case strings.HasSuffix(path, ".events.jsonl"):
		return strings.TrimSuffix(path, ".events.jsonl") + ".jsonl"
	case strings.HasSuffix(path, ".goal-state.json"):
		return strings.TrimSuffix(path, ".goal-state.json") + ".jsonl"
	case strings.HasSuffix(filepath.Base(filepath.Dir(path)), ".jobs"):
		return strings.TrimSuffix(filepath.Dir(path), ".jobs") + ".jsonl"
	default:
		return ""
	}
}

func sessionRedactionLeaseHeld(sessionPath string) bool {
	if agent.SessionLeaseHeld(sessionPath) {
		return true
	}
	if strings.HasSuffix(sessionPath, ".guardian.jsonl") {
		parent := strings.TrimSuffix(sessionPath, ".guardian.jsonl") + ".jsonl"
		return agent.SessionLeaseHeld(parent)
	}
	return false
}

func redactSessionFile(path string, dryRun bool) (changed bool, bytesRewritten int64, err error) {
	info, err := os.Stat(path)
	if err != nil {
		return false, 0, err
	}
	if info.IsDir() {
		return false, 0, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return false, 0, err
	}
	next := []byte(secrets.Redact(string(raw)))
	if string(raw) == string(next) {
		return false, 0, nil
	}
	if dryRun {
		return true, int64(len(next)), nil
	}
	perm := info.Mode().Perm()
	if perm == 0 {
		perm = 0o600
	}
	if err := fileutil.AtomicWriteFile(path, next, perm); err != nil {
		return false, 0, err
	}
	return true, int64(len(next)), nil
}

func removeDerivedSessionIndex(changedPath string) {
	sessionPath := redactionSessionPath(changedPath)
	if sessionPath == "" {
		return
	}
	if err := os.Remove(store.SessionEventIndex(sessionPath)); err != nil && !os.IsNotExist(err) {
		return
	}
}
