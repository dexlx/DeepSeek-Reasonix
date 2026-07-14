package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckReportsInvalidProjectConfig(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "reasonix.toml"), []byte("[broken\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"check", "--root", root, "--json"}); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
}
