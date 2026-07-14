package repair

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"reasonix/internal/config"
	textdiff "reasonix/internal/diff"
)

const RepairPlanSchemaVersion = 1

type RepairPlan struct {
	SchemaVersion int                `json:"schemaVersion"`
	Summary       string             `json:"summary"`
	Actions       []RepairPlanAction `json:"actions"`
}

type RepairPlanAction struct {
	Type       string `json:"type"`
	Scope      string `json:"scope,omitempty"`
	SnapshotID string `json:"snapshotId,omitempty"`
	Target     string `json:"target,omitempty"`
	Reason     string `json:"reason"`
}

type RepairPlanPreview struct {
	Index       int    `json:"index"`
	Type        string `json:"type"`
	Description string `json:"description"`
	Diff        string `json:"diff,omitempty"`
}

type ApplyPlanOptions struct {
	Root         string
	AllowProject bool
}

type ApplyPlanResult struct {
	Applied []string `json:"applied"`
}

func DecodeRepairPlan(data []byte) (RepairPlan, error) {
	data = bytes.TrimSpace(data)
	if bytes.HasPrefix(data, []byte("```")) {
		if start := bytes.IndexByte(data, '{'); start >= 0 {
			if end := bytes.LastIndexByte(data, '}'); end >= start {
				data = data[start : end+1]
			}
		}
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var plan RepairPlan
	if err := dec.Decode(&plan); err != nil {
		return RepairPlan{}, fmt.Errorf("decode repair plan: %w", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		if err == nil {
			return RepairPlan{}, fmt.Errorf("decode repair plan: trailing JSON")
		}
		return RepairPlan{}, fmt.Errorf("decode repair plan: %w", err)
	}
	if err := ValidateRepairPlan(plan); err != nil {
		return RepairPlan{}, err
	}
	return plan, nil
}

func ValidateRepairPlan(plan RepairPlan) error {
	if plan.SchemaVersion != RepairPlanSchemaVersion {
		return fmt.Errorf("repair plan schemaVersion must be %d", RepairPlanSchemaVersion)
	}
	if len(plan.Actions) > 8 {
		return fmt.Errorf("repair plan must contain at most 8 actions")
	}
	if len(plan.Summary) > 1000 {
		return fmt.Errorf("repair plan summary is too long")
	}
	if containsPlanControl(plan.Summary) {
		return fmt.Errorf("repair plan summary contains control characters")
	}
	for i, action := range plan.Actions {
		if len(action.Reason) > 500 {
			return fmt.Errorf("repair action %d reason is too long", i+1)
		}
		if containsPlanControl(action.Reason) {
			return fmt.Errorf("repair action %d reason contains control characters", i+1)
		}
		switch action.Type {
		case "repair_config":
			if action.Scope != "global" && action.Scope != "project" {
				return fmt.Errorf("repair action %d: repair_config scope must be global or project", i+1)
			}
			if action.SnapshotID != "" || action.Target != "" {
				return fmt.Errorf("repair action %d: repair_config has unexpected parameters", i+1)
			}
		case "restore_snapshot":
			if strings.TrimSpace(action.SnapshotID) == "" || action.Scope != "" || action.Target != "" {
				return fmt.Errorf("repair action %d: restore_snapshot requires only snapshotId", i+1)
			}
		case "rebuild_derived_state":
			switch action.Target {
			case "tabs", "projects", "window", "zoom", "all":
			default:
				return fmt.Errorf("repair action %d: invalid derived-state target", i+1)
			}
			if action.Scope != "" || action.SnapshotID != "" {
				return fmt.Errorf("repair action %d: rebuild_derived_state has unexpected parameters", i+1)
			}
		case "rollback_update":
			if action.Scope != "" || action.SnapshotID != "" || action.Target != "" {
				return fmt.Errorf("repair action %d: rollback_update takes no parameters", i+1)
			}
		default:
			return fmt.Errorf("repair action %d: type %q is not allowed", i+1, action.Type)
		}
	}
	return nil
}

func containsPlanControl(text string) bool {
	for _, r := range text {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

func PreviewRepairPlan(plan RepairPlan, opts ApplyPlanOptions) ([]RepairPlanPreview, error) {
	if err := ValidateRepairPlan(plan); err != nil {
		return nil, err
	}
	previews := make([]RepairPlanPreview, 0, len(plan.Actions))
	for i, action := range plan.Actions {
		preview := RepairPlanPreview{Index: i + 1, Type: action.Type}
		switch action.Type {
		case "repair_config":
			if action.Scope == "project" && !opts.AllowProject {
				return nil, fmt.Errorf("action %d requires --allow-project", i+1)
			}
			path := config.UserConfigPath()
			if action.Scope == "project" {
				path = projectConfigPath(opts.Root)
			}
			before, _ := os.ReadFile(path)
			after := []byte{}
			if action.Scope == "global" {
				after, _ = os.ReadFile(lastKnownGoodConfigPath())
			}
			preview.Description = "Quarantine invalid " + action.Scope + " configuration"
			preview.Diff = textdiff.Build(action.Scope+"-config.toml", string(before), string(after), textdiff.Modify).Diff
		case "restore_snapshot":
			snap, err := configSnapshotByID(action.SnapshotID)
			if err != nil {
				return nil, err
			}
			if err := verifyConfigSnapshot(snap); err != nil {
				return nil, err
			}
			before, _ := os.ReadFile(config.UserConfigPath())
			after, _ := os.ReadFile(snap.Path)
			preview.Description = "Restore verified global configuration snapshot " + snap.ID
			preview.Diff = textdiff.Build("global-config.toml", string(before), string(after), textdiff.Modify).Diff
		case "rebuild_derived_state":
			preview.Description = "Quarantine and rebuild derived desktop state: " + action.Target
		case "rollback_update":
			tx, err := ReadPendingUpdate()
			if err != nil {
				return nil, fmt.Errorf("action %d: no rollback-ready update: %w", i+1, err)
			}
			preview.Description = fmt.Sprintf("Restore Reasonix %s over probationary %s", tx.FromVersion, tx.ToVersion)
		}
		previews = append(previews, preview)
	}
	return previews, nil
}

func ApplyRepairPlan(plan RepairPlan, opts ApplyPlanOptions) (ApplyPlanResult, error) {
	if _, err := PreviewRepairPlan(plan, opts); err != nil {
		return ApplyPlanResult{Applied: []string{}}, err
	}
	result := ApplyPlanResult{Applied: []string{}}
	for i, action := range plan.Actions {
		switch action.Type {
		case "repair_config":
			report, err := InspectAndRepairConfig(ConfigOptions{Root: opts.Root, Apply: true, IncludeProject: action.Scope == "project", OnlyScope: action.Scope})
			if err != nil {
				return result, fmt.Errorf("action %d: %w", i+1, err)
			}
			result.Applied = append(result.Applied, report.Applied...)
		case "restore_snapshot":
			tx, err := RestoreConfigSnapshot(action.SnapshotID)
			if err != nil {
				return result, fmt.Errorf("action %d: %w", i+1, err)
			}
			result.Applied = append(result.Applied, "restored config snapshot (undo "+tx.ID+")")
		case "rebuild_derived_state":
			paths, err := RebuildDerivedState(action.Target)
			if err != nil {
				return result, fmt.Errorf("action %d: %w", i+1, err)
			}
			result.Applied = append(result.Applied, paths...)
		case "rollback_update":
			rollback, err := RollbackPendingUpdate()
			if err != nil {
				return result, fmt.Errorf("action %d: %w", i+1, err)
			}
			if rollback.RolledBack {
				result.Applied = append(result.Applied, "rolled back update to "+rollback.ToVersion)
			}
		}
	}
	return result, nil
}

func configSnapshotByID(id string) (ConfigSnapshot, error) {
	snapshots, err := ListConfigSnapshots()
	if err != nil {
		return ConfigSnapshot{}, err
	}
	for _, snap := range snapshots {
		if snap.ID == id {
			return snap, nil
		}
	}
	return ConfigSnapshot{}, fmt.Errorf("config snapshot %q not found", id)
}

func projectConfigPath(root string) string {
	root = strings.TrimSpace(root)
	if root == "" || root == "." {
		return "reasonix.toml"
	}
	return filepath.Join(root, "reasonix.toml")
}
