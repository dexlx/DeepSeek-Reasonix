package repair

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestStartupTrackerRecommendsSafeModeAfterCrashLoop(t *testing.T) {
	now := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	tracker := NewStartupTracker(filepath.Join(t.TempDir(), "startup.json"))
	tracker.now = func() time.Time { return now }
	tracker.processAlive = func(int) bool { return false }
	if _, err := tracker.Begin("v1", false); err != nil {
		t.Fatal(err)
	}
	if tracker.SafeModeRecommended() {
		t.Fatal("safe mode recommended after only one failed start")
	}
	now = now.Add(time.Minute)
	if _, err := tracker.Begin("v1", false); err != nil {
		t.Fatal(err)
	}
	if !tracker.SafeModeRecommended() {
		t.Fatal("safe mode not recommended for the third start in the crash window")
	}
}

func TestStartupTrackerHealthyResetsCrashLoop(t *testing.T) {
	tracker := NewStartupTracker(filepath.Join(t.TempDir(), "startup.json"))
	tracker.processAlive = func(int) bool { return false }
	if _, err := tracker.Begin("v1", false); err != nil {
		t.Fatal(err)
	}
	if err := tracker.MarkReady(); err != nil {
		t.Fatal(err)
	}
	state, err := tracker.Read()
	if err != nil {
		t.Fatal(err)
	}
	if state.Phase != "ready" || state.ConsecutiveFailures != 1 {
		t.Fatalf("state = %+v", state)
	}
	if err := tracker.MarkHealthy(); err != nil {
		t.Fatal(err)
	}
	state, err = tracker.Read()
	if err != nil {
		t.Fatal(err)
	}
	if state.Phase != "healthy" || state.ConsecutiveFailures != 0 {
		t.Fatalf("healthy state = %+v", state)
	}
	if err := tracker.MarkFailed(errors.New("boom")); err != nil {
		t.Fatal(err)
	}
}

func TestStartupTrackerCountsExplicitFailures(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	tracker := NewStartupTracker(filepath.Join(t.TempDir(), "startup.json"))
	tracker.now = func() time.Time { return now }
	tracker.processAlive = func(int) bool { return false }
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := tracker.Begin("v1", false); err != nil {
			t.Fatal(err)
		}
		if err := tracker.MarkFailed(errors.New("wails failed")); err != nil {
			t.Fatal(err)
		}
		now = now.Add(time.Minute)
	}
	if !tracker.SafeModeRecommended() {
		t.Fatal("explicit failed phases did not trigger safe mode recommendation")
	}
}

func TestStartupTrackerDoesNotCountLiveDuplicateLaunch(t *testing.T) {
	tracker := NewStartupTracker(filepath.Join(t.TempDir(), "startup.json"))
	tracker.processAlive = func(pid int) bool { return pid == 42 }
	state := StartupState{SchemaVersion: 1, Phase: "starting", PID: 42, ConsecutiveFailures: 1, WindowStartedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if err := tracker.write(state); err != nil {
		t.Fatal(err)
	}
	got, err := tracker.Begin("v2", false)
	if err != nil {
		t.Fatal(err)
	}
	if got.PID != 42 || got.ConsecutiveFailures != 1 {
		t.Fatalf("live startup was overwritten: %+v", got)
	}
}

func TestStartupTrackerDoesNotOverwriteLiveReadyInstance(t *testing.T) {
	tracker := NewStartupTracker(filepath.Join(t.TempDir(), "startup.json"))
	tracker.processAlive = func(pid int) bool { return pid == 42 }
	state := StartupState{SchemaVersion: 1, Phase: "ready", PID: 42, Version: "v1"}
	if err := tracker.write(state); err != nil {
		t.Fatal(err)
	}
	got, err := tracker.Begin("v2", false)
	if err != nil {
		t.Fatal(err)
	}
	if got.PID != 42 || got.Version != "v1" || got.Phase != "ready" {
		t.Fatalf("live ready instance was overwritten: %+v", got)
	}
}

func TestStartupTrackerWithoutStateDirIsDisabled(t *testing.T) {
	tracker := &StartupTracker{now: time.Now, processAlive: func(int) bool { return false }}
	if _, err := tracker.Begin("v1", false); err != nil {
		t.Fatal(err)
	}
	if tracker.Path() != "" {
		t.Fatalf("path = %q", tracker.Path())
	}
}
