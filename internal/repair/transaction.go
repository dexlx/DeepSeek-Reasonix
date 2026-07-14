package repair

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/fileutil"
)

type RepairChange struct {
	Scope        string `json:"scope,omitempty"`
	TargetPath   string `json:"targetPath"`
	PreviousPath string `json:"previousPath,omitempty"`
	RemoveOnUndo bool   `json:"removeOnUndo,omitempty"`
}

type RepairTransaction struct {
	SchemaVersion int            `json:"schemaVersion"`
	ID            string         `json:"id"`
	CreatedAt     string         `json:"createdAt"`
	Changes       []RepairChange `json:"changes"`
	Undone        bool           `json:"undone,omitempty"`
	UndoneAt      string         `json:"undoneAt,omitempty"`
}

func newRepairTransaction(now time.Time) *RepairTransaction {
	now = now.UTC()
	return &RepairTransaction{
		SchemaVersion: 1,
		ID:            fmt.Sprintf("repair-%d", now.UnixNano()),
		CreatedAt:     now.Format(time.RFC3339Nano),
		Changes:       []RepairChange{},
	}
}

func repairTransactionPath() string {
	if root := config.MemoryUserDir(); root != "" {
		return filepath.Join(root, "repair", "last-repair.json")
	}
	return ""
}

func repairLogPath() string {
	if root := config.MemoryUserDir(); root != "" {
		return filepath.Join(root, "repair", "repair-log.jsonl")
	}
	return ""
}

func saveRepairTransaction(tx *RepairTransaction) error {
	if tx == nil || len(tx.Changes) == 0 {
		return nil
	}
	if err := persistRepairTransaction(tx); err != nil {
		return err
	}
	return appendRepairLog(tx)
}

func persistRepairTransaction(tx *RepairTransaction) error {
	path := repairTransactionPath()
	if path == "" {
		return nil
	}
	b, err := json.MarshalIndent(tx, "", "  ")
	if err != nil {
		return err
	}
	return fileutil.AtomicWriteFile(path, append(b, '\n'), 0o600)
}

func appendRepairLog(tx *RepairTransaction) error {
	path := repairLogPath()
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(tx)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(b, '\n'))
	return err
}

func ReadLastRepair() (*RepairTransaction, error) {
	path := repairTransactionPath()
	if path == "" {
		return nil, os.ErrNotExist
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var tx RepairTransaction
	if err := json.Unmarshal(b, &tx); err != nil {
		return nil, err
	}
	if tx.SchemaVersion != 1 || tx.ID == "" || len(tx.Changes) == 0 {
		return nil, fmt.Errorf("last repair transaction is incomplete")
	}
	for _, change := range tx.Changes {
		if err := validateRepairChange(change); err != nil {
			return nil, err
		}
	}
	return &tx, nil
}

func validateRepairChange(change RepairChange) error {
	target := filepath.Clean(change.TargetPath)
	switch {
	case change.Scope == "global":
		if target != filepath.Clean(config.UserConfigPath()) {
			return fmt.Errorf("repair transaction global target is invalid")
		}
	case change.Scope == "project":
		if filepath.Base(target) != "reasonix.toml" {
			return fmt.Errorf("repair transaction project target is invalid")
		}
	case strings.HasPrefix(change.Scope, "derived:"):
		name := change.Scope[len("derived:"):]
		want, ok := derivedStatePaths()[name]
		if !ok || target != filepath.Clean(want) {
			return fmt.Errorf("repair transaction derived-state target is invalid")
		}
	default:
		return fmt.Errorf("repair transaction scope is invalid")
	}
	if change.RemoveOnUndo {
		if change.Scope != "global" || change.PreviousPath != "" {
			return fmt.Errorf("repair transaction remove-on-undo action is invalid")
		}
		return nil
	}
	previous := filepath.Clean(change.PreviousPath)
	if filepath.Dir(previous) == filepath.Dir(target) && strings.HasPrefix(filepath.Base(previous), filepath.Base(target)+".reasonix-") {
		return nil
	}
	if config.MemoryUserDir() == "" {
		return fmt.Errorf("repair transaction state directory is unavailable")
	}
	restoreRoot := filepath.Join(config.MemoryUserDir(), "repair", "restore-backups")
	rel, err := filepath.Rel(filepath.Clean(restoreRoot), previous)
	if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil
	}
	return fmt.Errorf("repair transaction previous path is invalid")
}

// UndoLastRepair restores the exact files moved aside by the latest repair. Any
// currently repaired file is retained as a timestamped redo candidate.
func UndoLastRepair() (*RepairTransaction, error) {
	tx, err := ReadLastRepair()
	if err != nil {
		return nil, err
	}
	if tx.Undone {
		return nil, fmt.Errorf("repair %s was already undone", tx.ID)
	}
	now := time.Now().UTC()
	for _, change := range tx.Changes {
		if change.RemoveOnUndo {
			continue
		}
		if _, err := os.Stat(change.PreviousPath); err != nil {
			return nil, fmt.Errorf("undo repair: previous file %s: %w", change.PreviousPath, err)
		}
	}
	for i := len(tx.Changes) - 1; i >= 0; i-- {
		change := tx.Changes[i]
		redo := ""
		if _, err := os.Stat(change.TargetPath); err == nil {
			redo = change.TargetPath + ".reasonix-redo-" + now.Format("20060102T150405Z")
			if err := os.Rename(change.TargetPath, redo); err != nil {
				return nil, fmt.Errorf("undo repair: retain current file: %w", err)
			}
		}
		if change.RemoveOnUndo {
			continue
		}
		if isRestoreBackupPath(change.PreviousPath) {
			b, err := os.ReadFile(change.PreviousPath)
			if err == nil {
				err = fileutil.AtomicWriteFile(change.TargetPath, b, 0o600)
			}
			if err != nil {
				if redo != "" {
					_ = os.Rename(redo, change.TargetPath)
				}
				return nil, fmt.Errorf("undo repair: restore %s: %w", change.TargetPath, err)
			}
			_ = os.Remove(change.PreviousPath)
			continue
		}
		if err := os.Rename(change.PreviousPath, change.TargetPath); err != nil {
			if redo != "" {
				_ = os.Rename(redo, change.TargetPath)
			}
			return nil, fmt.Errorf("undo repair: restore %s: %w", change.TargetPath, err)
		}
	}
	tx.Undone = true
	tx.UndoneAt = now.Format(time.RFC3339Nano)
	if err := saveRepairTransaction(tx); err != nil {
		return nil, err
	}
	return tx, nil
}

func isRestoreBackupPath(path string) bool {
	root := config.MemoryUserDir()
	if root == "" {
		return false
	}
	restoreRoot := filepath.Join(root, "repair", "restore-backups")
	rel, err := filepath.Rel(filepath.Clean(restoreRoot), filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
