package repair

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPendingUpdateRejectsTargetOutsideGuardInstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	guardDir := t.TempDir()
	target := filepath.Join(t.TempDir(), "reasonix-desktop")
	backup := filepath.Join(home, "repair", "updates", "reasonix-desktop.previous")
	if err := os.MkdirAll(filepath.Dir(backup), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backup, []byte("old"), 0o700); err != nil {
		t.Fatal(err)
	}
	tx := &UpdateTransaction{SchemaVersion: 1, ToVersion: "v2", TargetKind: "file", TargetPath: target, BackupPath: backup, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if err := WritePendingUpdate(tx); err != nil {
		t.Fatal(err)
	}
	originalExecutable := repairExecutable
	repairExecutable = func() (string, error) { return filepath.Join(guardDir, "reasonix-guard"), nil }
	t.Cleanup(func() { repairExecutable = originalExecutable })
	if _, err := ReadPendingUpdate(); err == nil {
		t.Fatal("pending update outside Guard install was accepted")
	}
}
