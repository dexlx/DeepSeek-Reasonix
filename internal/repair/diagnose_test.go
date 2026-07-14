package repair

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDiagnoseFindsProviderPluginAndPermissionProblems(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	configText := `default_model = "custom/missing"

[[providers]]
name = "custom"
kind = "openai"
base_url = "not-a-url"
model = "model-a"
api_key_env = "CUSTOM_KEY"

[[plugins]]
name = "missing-command"
command = "reasonix-command-that-does-not-exist"

[permissions]
allow = ["bash"]
deny = ["bash"]
`
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(configText), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := Diagnose(context.Background(), DiagnoseOptions{Root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	codes := map[string]bool{}
	for _, finding := range report.Findings {
		codes[finding.Code] = true
	}
	for _, want := range []string{"provider.invalid_url", "provider.missing_key", "model.invalid_default", "plugin.command_missing", "permissions.conflict"} {
		if !codes[want] {
			t.Errorf("missing finding %s: %+v", want, report.Findings)
		}
	}
}

func TestRebuildDerivedStateQuarantinesWithoutDeleting(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	path := filepath.Join(home, "desktop-tabs.json")
	if err := os.WriteFile(path, []byte("bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	applied, err := RebuildDerivedState("tabs")
	if err != nil {
		t.Fatal(err)
	}
	if len(applied) != 1 {
		t.Fatalf("applied = %v", applied)
	}
	if _, err := os.Stat(applied[0]); err != nil {
		t.Fatalf("quarantined state missing: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("original derived state remains: %v", err)
	}
}
