package main

import (
	"strings"
	"testing"

	"reasonix/internal/repair"
)

func TestSanitizeDiagnosticReportRemovesWorkspacePath(t *testing.T) {
	root := t.TempDir()
	report := repair.DiagnosticReport{Root: root, Findings: []repair.DiagnosticFinding{{Message: "bad file " + root + "/reasonix.toml", Remediation: "edit " + root}}}
	got := sanitizeDiagnosticReport(report)
	b := got.Findings[0].Message + got.Findings[0].Remediation + got.Root
	if strings.Contains(b, root) {
		t.Fatalf("sanitized report leaked root: %s", b)
	}
}
