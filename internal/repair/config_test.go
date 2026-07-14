package repair

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestInspectInvalidProjectConfigIsReadOnlyByDefault(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "reasonix.toml")
	if err := os.WriteFile(path, []byte("[broken\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := InspectAndRepairConfig(ConfigOptions{Root: root, Apply: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Checks) != 2 || report.Checks[1].Valid {
		t.Fatalf("checks = %+v", report.Checks)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("project config was modified without IncludeProject: %v", err)
	}
}

func TestInspectCanQuarantineInvalidProjectConfig(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "reasonix.toml")
	if err := os.WriteFile(path, []byte("[broken\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := InspectAndRepairConfig(ConfigOptions{Root: root, Apply: true, IncludeProject: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.Checks[1].Exists || !report.Checks[1].Valid {
		t.Fatalf("project check after repair = %+v", report.Checks[1])
	}
	if matches, _ := filepath.Glob(path + ".reasonix-quarantine-*"); len(matches) != 1 {
		t.Fatalf("quarantine matches = %v", matches)
	}
}

func TestRepairRestoresLastKnownGoodGlobalConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	path := filepath.Join(home, "config.toml")
	original := []byte("default_model = \"deepseek-flash\"\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RecordHealthyConfig("v1"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("[broken\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := InspectAndRepairConfig(ConfigOptions{Root: t.TempDir(), Apply: true})
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("restored config = %q, want %q", got, original)
	}
	if len(report.Applied) != 2 {
		t.Fatalf("applied = %v", report.Applied)
	}
}

func TestConfigSnapshotsRotateAndVerifyHash(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	path := filepath.Join(home, "config.toml")
	for i := 0; i < configSnapshotRetention+2; i++ {
		content := []byte("default_model = \"model-" + string(rune('a'+i)) + "\"\n")
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := RecordHealthyConfig("v1"); err != nil {
			t.Fatal(err)
		}
	}
	snapshots, err := ListConfigSnapshots()
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != configSnapshotRetention {
		t.Fatalf("snapshots = %d, want %d", len(snapshots), configSnapshotRetention)
	}
	if err := os.WriteFile(snapshots[0].Path, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreConfigSnapshot(snapshots[0].ID); err == nil {
		t.Fatal("tampered snapshot was restored")
	}
}

func TestUndoRepairRestoresQuarantinedConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	path := filepath.Join(home, "config.toml")
	bad := []byte("[broken\n")
	if err := os.WriteFile(path, bad, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectAndRepairConfig(ConfigOptions{Root: t.TempDir(), Apply: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := UndoLastRepair(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(bad) {
		t.Fatalf("undone config = %q", got)
	}
}

func TestUndoRejectsTamperedRepairTarget(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	previous := filepath.Join(home, "unrelated.previous")
	if err := os.WriteFile(previous, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	tx := newRepairTransaction(time.Now())
	tx.Changes = append(tx.Changes, RepairChange{Scope: "global", TargetPath: filepath.Join(t.TempDir(), "arbitrary.txt"), PreviousPath: previous})
	if err := persistRepairTransaction(tx); err != nil {
		t.Fatal(err)
	}
	if _, err := UndoLastRepair(); err == nil {
		t.Fatal("tampered repair transaction was accepted")
	}
}

func TestSnapshotUndoAcrossSeparateStateHome(t *testing.T) {
	home := t.TempDir()
	stateHome := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	t.Setenv("REASONIX_STATE_HOME", stateHome)
	path := filepath.Join(home, "config.toml")
	if err := os.WriteFile(path, []byte("default_model = \"before\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RecordHealthyConfig("v1"); err != nil {
		t.Fatal(err)
	}
	snapshots, err := ListConfigSnapshots()
	if err != nil || len(snapshots) != 1 {
		t.Fatalf("snapshots = %+v, err = %v", snapshots, err)
	}
	current := []byte("default_model = \"current\"\n")
	if err := os.WriteFile(path, current, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreConfigSnapshot(snapshots[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := UndoLastRepair(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(current) {
		t.Fatalf("undo restored %q, want %q", got, current)
	}
}
