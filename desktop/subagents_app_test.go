package main

import (
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/skill"
)

func newTestSubagentApp(t *testing.T) *App {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("AppData", filepath.Join(home, "AppData"))
	st := skill.New(skill.Options{HomeDir: home})
	a := NewApp()
	a.setTestCtrl(control.New(control.Options{AllSkillStore: st, SkillStore: st}), "")
	t.Cleanup(func() { a.activeCtrl().Close() })
	return a
}

func TestCreateSubagentProfileWritesManualInvocationSubagentSkill(t *testing.T) {
	a := newTestSubagentApp(t)
	path, err := a.CreateSubagentProfile(SubagentProfileInput{
		Name:         "my-formatter",
		Description:  "formats code the way I like",
		SystemPrompt: "You are a code formatting assistant.",
		Color:        "amber",
		AllowedTools: []string{"read_file", "edit_file"},
		Scope:        "global",
	})
	if err != nil {
		t.Fatalf("CreateSubagentProfile: %v", err)
	}
	if path == "" {
		t.Fatal("expected a non-empty path")
	}

	views := a.SkillsSettings().Skills
	var found *SkillView
	for i := range views {
		if views[i].Name == "my-formatter" {
			found = &views[i]
		}
	}
	if found == nil {
		t.Fatalf("created profile missing from SkillsSettings: %+v", views)
	}
	if found.RunAs != "subagent" || found.Invocation != "manual" || found.Color != "amber" {
		t.Fatalf("profile fields wrong: %+v", found)
	}
}

func TestCreateSubagentProfileRejectsBuiltinNameCollision(t *testing.T) {
	a := newTestSubagentApp(t)
	_, err := a.CreateSubagentProfile(SubagentProfileInput{
		Name:         "explore",
		Description:  "shadow the built-in",
		SystemPrompt: "do something else entirely",
	})
	if err == nil {
		t.Fatal("expected an error naming a built-in subagent")
	}
}

func TestCreateSubagentProfileRejectsDuplicateName(t *testing.T) {
	a := newTestSubagentApp(t)
	input := SubagentProfileInput{Name: "dup", Description: "first", SystemPrompt: "body"}
	if _, err := a.CreateSubagentProfile(input); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if _, err := a.CreateSubagentProfile(input); err == nil {
		t.Fatal("expected an error creating a duplicate name")
	}
}

func TestCreateSubagentProfileRequiresDescriptionAndPrompt(t *testing.T) {
	a := newTestSubagentApp(t)
	if _, err := a.CreateSubagentProfile(SubagentProfileInput{Name: "x", SystemPrompt: "body"}); err == nil {
		t.Error("expected an error for a missing description")
	}
	if _, err := a.CreateSubagentProfile(SubagentProfileInput{Name: "x", Description: "d"}); err == nil {
		t.Error("expected an error for a missing system prompt")
	}
}

func TestUpdateSubagentProfileOverwritesFields(t *testing.T) {
	a := newTestSubagentApp(t)
	if _, err := a.CreateSubagentProfile(SubagentProfileInput{
		Name: "editable-agent", Description: "v1", SystemPrompt: "old body", Color: "amber", Scope: "global",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := a.UpdateSubagentProfile("editable-agent", "global", SubagentProfileInput{
		Description: "v2", SystemPrompt: "new body", Color: "blue", Model: "deepseek/deepseek-pro", AllowedTools: []string{"read_file"},
	}); err != nil {
		t.Fatalf("UpdateSubagentProfile: %v", err)
	}
	var found *SkillView
	for _, sk := range a.SkillsSettings().Skills {
		if sk.Name == "editable-agent" {
			found = &sk
		}
	}
	if found == nil {
		t.Fatal("editable-agent missing after update")
	}
	if found.Description != "v2" || found.Color != "blue" || found.Model != "deepseek/deepseek-pro" || found.Invocation != "manual" || found.RunAs != "subagent" {
		t.Fatalf("update did not apply as expected: %+v", found)
	}
	if len(found.AllowedTools) != 1 || found.AllowedTools[0] != "read_file" {
		t.Fatalf("AllowedTools not updated: %v", found.AllowedTools)
	}
}

func TestUpdateSubagentProfileRequiresDescriptionAndPrompt(t *testing.T) {
	a := newTestSubagentApp(t)
	if _, err := a.CreateSubagentProfile(SubagentProfileInput{
		Name: "editable-agent2", Description: "v1", SystemPrompt: "old body", Scope: "global",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := a.UpdateSubagentProfile("editable-agent2", "global", SubagentProfileInput{SystemPrompt: "new body"}); err == nil {
		t.Error("expected an error for a missing description")
	}
	if err := a.UpdateSubagentProfile("editable-agent2", "global", SubagentProfileInput{Description: "d"}); err == nil {
		t.Error("expected an error for a missing system prompt")
	}
}

func TestUpdateSubagentProfileWrongScopeFailsSafely(t *testing.T) {
	a := newTestSubagentApp(t)
	if _, err := a.CreateSubagentProfile(SubagentProfileInput{
		Name: "editable-agent3", Description: "v1", SystemPrompt: "old body", Scope: "global",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := a.UpdateSubagentProfile("editable-agent3", "project", SubagentProfileInput{Description: "v2", SystemPrompt: "new body"}); err == nil {
		t.Fatal("expected an error updating with the wrong scope")
	}
	for _, sk := range a.SkillsSettings().Skills {
		if sk.Name == "editable-agent3" && sk.Description != "v1" {
			t.Fatalf("profile should be unchanged after a refused scope-mismatched update, got description=%q", sk.Description)
		}
	}
}

func TestTrySubagentProfileRequiresTaskAndPrompt(t *testing.T) {
	isolateDesktopUserDirs(t)
	a := NewApp()
	if _, err := a.TrySubagentProfile(SubagentProfileInput{SystemPrompt: "be helpful"}, ""); err == nil {
		t.Error("expected an error for a missing task")
	}
	if _, err := a.TrySubagentProfile(SubagentProfileInput{}, "do something"); err == nil {
		t.Error("expected an error for a missing system prompt")
	}
}

func TestTrySubagentProfileRejectsUnknownModel(t *testing.T) {
	isolateDesktopUserDirs(t)
	a := NewApp()
	_, err := a.TrySubagentProfile(SubagentProfileInput{
		SystemPrompt: "be helpful",
		Model:        "nope/does-not-exist",
	}, "do something")
	if err == nil {
		t.Error("expected an error for an unresolvable model ref")
	}
}

func TestDeleteSubagentProfileRemovesIt(t *testing.T) {
	a := newTestSubagentApp(t)
	if _, err := a.CreateSubagentProfile(SubagentProfileInput{
		Name: "temp-agent", Description: "d", SystemPrompt: "body", Scope: "global",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := a.DeleteSubagentProfile("temp-agent", "global"); err != nil {
		t.Fatalf("DeleteSubagentProfile: %v", err)
	}
	for _, sk := range a.SkillsSettings().Skills {
		if sk.Name == "temp-agent" {
			t.Fatal("deleted profile still present")
		}
	}
}

func TestSetSubagentProfileModelAndEffortRoundTripPerName(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "DEEPSEEK_API_KEY", "sk-test")
	if err := os.MkdirAll(filepath.Dir(config.UserConfigPath()), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(config.UserConfigPath(), []byte(`
default_model = "deepseek/deepseek-v4-flash"

[[providers]]
name = "deepseek"
kind = "openai"
base_url = "https://api.deepseek.com"
models = ["deepseek-v4-flash", "deepseek-v4-pro"]
default = "deepseek-v4-flash"
api_key_env = "DEEPSEEK_API_KEY"
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	app := NewApp()
	if err := app.SetSubagentProfileModel("explore", "deepseek/deepseek-v4-pro"); err != nil {
		t.Fatalf("SetSubagentProfileModel: %v", err)
	}
	if err := app.SetSubagentProfileEffort("explore", "max"); err != nil {
		t.Fatalf("SetSubagentProfileEffort: %v", err)
	}

	cfg := config.LoadForEdit(config.UserConfigPath())
	if cfg.Agent.SubagentModels["explore"] != "deepseek/deepseek-v4-pro" || cfg.Agent.SubagentEfforts["explore"] != "max" {
		t.Fatalf("saved per-name overrides = model:%q effort:%q", cfg.Agent.SubagentModels["explore"], cfg.Agent.SubagentEfforts["explore"])
	}
	// A different skill name must be unaffected — this is a per-name map, not
	// a global default.
	if cfg.Agent.SubagentModel != "" || cfg.Agent.SubagentEffort != "" {
		t.Fatalf("global subagent defaults should be untouched: model:%q effort:%q", cfg.Agent.SubagentModel, cfg.Agent.SubagentEffort)
	}

	// Clearing (empty ref/level) removes the map entry rather than storing "".
	if err := app.SetSubagentProfileModel("explore", ""); err != nil {
		t.Fatalf("clear SetSubagentProfileModel: %v", err)
	}
	if err := app.SetSubagentProfileEffort("explore", ""); err != nil {
		t.Fatalf("clear SetSubagentProfileEffort: %v", err)
	}
	cfg = config.LoadForEdit(config.UserConfigPath())
	if _, ok := cfg.Agent.SubagentModels["explore"]; ok {
		t.Fatalf("cleared model override should be removed, got %+v", cfg.Agent.SubagentModels)
	}
	if _, ok := cfg.Agent.SubagentEfforts["explore"]; ok {
		t.Fatalf("cleared effort override should be removed, got %+v", cfg.Agent.SubagentEfforts)
	}
}

func TestSetSubagentProfileModelRejectsUnknownModel(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	if err := app.SetSubagentProfileModel("explore", "nope/does-not-exist"); err == nil {
		t.Error("expected an error for an unresolvable model ref")
	}
}

func TestSkillsSettingsSurfacesConfiguredModelOverride(t *testing.T) {
	a := newTestSubagentApp(t)
	setDesktopTestCredential(t, "DEEPSEEK_API_KEY", "sk-test")
	if err := os.MkdirAll(filepath.Dir(config.UserConfigPath()), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(config.UserConfigPath(), []byte(`
default_model = "deepseek/deepseek-v4-flash"

[[providers]]
name = "deepseek"
kind = "openai"
base_url = "https://api.deepseek.com"
models = ["deepseek-v4-flash", "deepseek-v4-pro"]
default = "deepseek-v4-flash"
api_key_env = "DEEPSEEK_API_KEY"

[agent.subagent_models]
explore = "deepseek/deepseek-v4-pro"

[agent.subagent_efforts]
explore = "max"
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	found := false
	for _, sk := range a.SkillsSettings().Skills {
		if sk.Name != "explore" {
			continue
		}
		found = true
		if sk.ConfiguredModel != "deepseek/deepseek-v4-pro" || sk.ConfiguredEffort != "max" {
			t.Fatalf("explore configured override = model:%q effort:%q", sk.ConfiguredModel, sk.ConfiguredEffort)
		}
	}
	if !found {
		t.Fatal("explore not present in SkillsSettings")
	}
}

func TestDeleteSubagentProfileWrongScopeFailsSafely(t *testing.T) {
	a := newTestSubagentApp(t)
	if _, err := a.CreateSubagentProfile(SubagentProfileInput{
		Name: "scoped-agent", Description: "d", SystemPrompt: "body", Scope: "global",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := a.DeleteSubagentProfile("scoped-agent", "project"); err == nil {
		t.Fatal("expected an error deleting with the wrong scope")
	}
	found := false
	for _, sk := range a.SkillsSettings().Skills {
		if sk.Name == "scoped-agent" {
			found = true
		}
	}
	if !found {
		t.Fatal("profile should survive a refused scope-mismatched delete")
	}
}
