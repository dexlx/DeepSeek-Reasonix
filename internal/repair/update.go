package repair

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/fileutil"
)

const updateTransactionVersion = 1

var repairExecutable = os.Executable

type UpdateTransaction struct {
	SchemaVersion int    `json:"schemaVersion"`
	FromVersion   string `json:"fromVersion,omitempty"`
	ToVersion     string `json:"toVersion"`
	Platform      string `json:"platform"`
	TargetKind    string `json:"targetKind"` // file | app-bundle
	TargetPath    string `json:"targetPath"`
	BackupPath    string `json:"backupPath"`
	BackupSHA256  string `json:"backupSha256,omitempty"`
	CreatedAt     string `json:"createdAt"`
}

type UpdateRollbackResult struct {
	RolledBack  bool   `json:"rolledBack"`
	FromVersion string `json:"fromVersion,omitempty"`
	ToVersion   string `json:"toVersion,omitempty"`
	TargetPath  string `json:"targetPath,omitempty"`
}

func PendingUpdatePath() string {
	root := config.MemoryUserDir()
	if root == "" {
		return ""
	}
	return filepath.Join(root, "repair", "pending-update.json")
}

// PrepareFileUpdate snapshots the current desktop executable and records an
// update transaction before an updater replaces it.
func PrepareFileUpdate(fromVersion, toVersion, targetPath string) (*UpdateTransaction, error) {
	targetPath = filepath.Clean(strings.TrimSpace(targetPath))
	if targetPath == "" || targetPath == "." {
		return nil, fmt.Errorf("prepare update: empty target path")
	}
	root := config.MemoryUserDir()
	if root == "" {
		return nil, fmt.Errorf("prepare update: Reasonix state directory is unavailable")
	}
	backupDir := filepath.Join(root, "repair", "updates")
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		return nil, err
	}
	backupPath := filepath.Join(backupDir, "reasonix-desktop.previous")
	hash, err := copyFileWithHash(targetPath, backupPath, 0o700)
	if err != nil {
		return nil, fmt.Errorf("prepare update backup: %w", err)
	}
	tx := &UpdateTransaction{
		SchemaVersion: updateTransactionVersion,
		FromVersion:   fromVersion,
		ToVersion:     toVersion,
		Platform:      runtime.GOOS + "/" + runtime.GOARCH,
		TargetKind:    "file",
		TargetPath:    targetPath,
		BackupPath:    backupPath,
		BackupSHA256:  hash,
		CreatedAt:     time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := WritePendingUpdate(tx); err != nil {
		return nil, err
	}
	return tx, nil
}

// PrepareAppBundleUpdate records the sibling bundle backup that the macOS
// handoff script creates. The script performs the directory move after exit.
func PrepareAppBundleUpdate(fromVersion, toVersion, appPath, backupPath string) (*UpdateTransaction, error) {
	tx := &UpdateTransaction{
		SchemaVersion: updateTransactionVersion,
		FromVersion:   fromVersion,
		ToVersion:     toVersion,
		Platform:      runtime.GOOS + "/" + runtime.GOARCH,
		TargetKind:    "app-bundle",
		TargetPath:    filepath.Clean(strings.TrimSpace(appPath)),
		BackupPath:    filepath.Clean(strings.TrimSpace(backupPath)),
		CreatedAt:     time.Now().UTC().Format(time.RFC3339Nano),
	}
	if !strings.HasSuffix(strings.ToLower(tx.TargetPath), ".app") || tx.BackupPath != tx.TargetPath+".reasonix-update-backup" {
		return nil, fmt.Errorf("prepare update: invalid macOS bundle paths")
	}
	if err := WritePendingUpdate(tx); err != nil {
		return nil, err
	}
	return tx, nil
}

func WritePendingUpdate(tx *UpdateTransaction) error {
	if tx == nil {
		return fmt.Errorf("pending update: nil transaction")
	}
	path := PendingUpdatePath()
	if path == "" {
		return fmt.Errorf("pending update: Reasonix state directory is unavailable")
	}
	b, err := json.MarshalIndent(tx, "", "  ")
	if err != nil {
		return err
	}
	return fileutil.AtomicWriteFile(path, append(b, '\n'), 0o600)
}

func ReadPendingUpdate() (*UpdateTransaction, error) {
	path := PendingUpdatePath()
	if path == "" {
		return nil, os.ErrNotExist
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var tx UpdateTransaction
	if err := json.Unmarshal(b, &tx); err != nil {
		return nil, err
	}
	if err := validateUpdateTransaction(&tx); err != nil {
		return nil, err
	}
	return &tx, nil
}

func HasPendingUpdate() bool {
	_, err := ReadPendingUpdate()
	return err == nil
}

// MarkUpdateHealthy commits a probationary update and removes its backup. A
// version mismatch is ignored so an older process cannot bless a newer update.
func MarkUpdateHealthy(runningVersion string) error {
	tx, err := ReadPendingUpdate()
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if strings.TrimSpace(runningVersion) != strings.TrimSpace(tx.ToVersion) {
		return nil
	}
	if err := os.Remove(PendingUpdatePath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	if tx.BackupPath != "" {
		if tx.TargetKind == "app-bundle" {
			_ = os.RemoveAll(tx.BackupPath)
		} else {
			_ = os.Remove(tx.BackupPath)
		}
	}
	return nil
}

// CancelPendingUpdate removes a transaction that failed before control was
// handed to the replacement build. A version mismatch is intentionally inert.
func CancelPendingUpdate(toVersion string) error {
	tx, err := ReadPendingUpdate()
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if strings.TrimSpace(toVersion) != strings.TrimSpace(tx.ToVersion) {
		return nil
	}
	if err := os.Remove(PendingUpdatePath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	if tx.TargetKind == "app-bundle" {
		_ = os.RemoveAll(tx.BackupPath)
	} else {
		_ = os.Remove(tx.BackupPath)
	}
	return nil
}

func RollbackPendingUpdate() (UpdateRollbackResult, error) {
	tx, err := ReadPendingUpdate()
	if err != nil {
		if os.IsNotExist(err) {
			return UpdateRollbackResult{}, nil
		}
		return UpdateRollbackResult{}, err
	}
	result := UpdateRollbackResult{FromVersion: tx.ToVersion, ToVersion: tx.FromVersion, TargetPath: tx.TargetPath}
	switch tx.TargetKind {
	case "file":
		if tx.BackupSHA256 != "" {
			got, hashErr := hashFile(tx.BackupPath)
			if hashErr != nil || !strings.EqualFold(got, tx.BackupSHA256) {
				return result, fmt.Errorf("rollback update: backup hash mismatch")
			}
		}
		mode := os.FileMode(0o700)
		if st, statErr := os.Stat(tx.TargetPath); statErr == nil {
			mode = st.Mode().Perm()
		}
		if _, err := copyFileWithHash(tx.BackupPath, tx.TargetPath, mode); err != nil {
			return result, fmt.Errorf("rollback update: %w", err)
		}
	case "app-bundle":
		if _, err := os.Stat(tx.BackupPath); err != nil {
			return result, fmt.Errorf("rollback update: backup bundle: %w", err)
		}
		failed := tx.TargetPath + ".reasonix-failed-" + time.Now().UTC().Format("20060102T150405Z")
		if err := os.Rename(tx.TargetPath, failed); err != nil {
			return result, fmt.Errorf("rollback update: move failed bundle: %w", err)
		}
		if err := os.Rename(tx.BackupPath, tx.TargetPath); err != nil {
			_ = os.Rename(failed, tx.TargetPath)
			return result, fmt.Errorf("rollback update: restore bundle: %w", err)
		}
	default:
		return result, fmt.Errorf("rollback update: unsupported target kind %q", tx.TargetKind)
	}
	result.RolledBack = true
	_ = os.Remove(PendingUpdatePath())
	return result, nil
}

func validateUpdateTransaction(tx *UpdateTransaction) error {
	if tx == nil || tx.SchemaVersion != updateTransactionVersion || strings.TrimSpace(tx.ToVersion) == "" {
		return fmt.Errorf("pending update metadata is incomplete")
	}
	tx.TargetPath = filepath.Clean(tx.TargetPath)
	tx.BackupPath = filepath.Clean(tx.BackupPath)
	launcher, err := repairExecutable()
	if err != nil {
		return fmt.Errorf("pending update launcher path is unavailable")
	}
	if resolved, resolveErr := filepath.EvalSymlinks(launcher); resolveErr == nil {
		launcher = resolved
	}
	launcher = filepath.Clean(launcher)
	switch tx.TargetKind {
	case "file":
		base := strings.ToLower(filepath.Base(tx.TargetPath))
		if base != "reasonix-desktop" && base != "reasonix-desktop.exe" && base != "reasonix.exe" {
			return fmt.Errorf("pending update target is not a Reasonix executable")
		}
		if filepath.Dir(launcher) != filepath.Dir(tx.TargetPath) {
			return fmt.Errorf("pending update target is outside the current Guard installation")
		}
		root := filepath.Clean(filepath.Join(config.MemoryUserDir(), "repair"))
		rel, err := filepath.Rel(root, tx.BackupPath)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("pending update backup is outside the repair directory")
		}
	case "app-bundle":
		if !strings.HasSuffix(strings.ToLower(tx.TargetPath), ".app") || tx.BackupPath != tx.TargetPath+".reasonix-update-backup" {
			return fmt.Errorf("pending update bundle paths are invalid")
		}
		inside := tx.TargetPath + string(filepath.Separator)
		if !strings.HasPrefix(launcher, inside) {
			return fmt.Errorf("pending update bundle is not the current Guard installation")
		}
	default:
		return fmt.Errorf("pending update target kind is invalid")
	}
	return nil
}

func copyFileWithHash(src, dst string, mode os.FileMode) (string, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".repair-copy-*")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), in); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := fileutil.ReplaceFile(tmpPath, dst); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
