package main

import (
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/repair"
)

// TestProviderSafeReportDropsUserControlledContent pins the outbound-privacy
// contract: the payload sent to the AI provider must carry only allowlisted
// codes, severities, and generic scopes — never diagnostic prose, which can
// quote config lines, MCP command lines, permission rules, URLs with
// credentials, emails, tokens, or absolute paths.
func TestProviderSafeReportDropsUserControlledContent(t *testing.T) {
	secrets := []string{
		"/Users/someone/dotfiles",
		"someone@example.com",
		"sk-live-Abc123Secret",
		"npx --api-key=sk-live-Abc123Secret mcp-server",
		"bash(rm -rf *)",
		"corp-internal-llm",
		"https://user:pass@proxy.internal:8080",
	}
	report := repair.DiagnosticReport{
		Root:    "/Users/someone/dotfiles",
		Network: true,
		Snapshots: []repair.DiagnosticSnapshot{
			{ID: "20260715T000000.000000000Z-abcdef123456", RecordedAt: "2026-07-15T00:00:00Z", Version: "v1"},
		},
		Findings: []repair.DiagnosticFinding{
			{Severity: "error", Code: "config.invalid_toml", Scope: "global",
				Message: "Configuration cannot be parsed: line 3: api_key = \"sk-live-Abc123Secret\" contact someone@example.com"},
			{Severity: "warning", Code: "plugin.command_missing", Scope: "plugin:corp-internal-llm",
				Message: `MCP command "npx --api-key=sk-live-Abc123Secret mcp-server" was not found.`},
			{Severity: "warning", Code: "permissions.conflict", Scope: "permissions",
				Message: `Permission rule "bash(rm -rf *)" appears in allow, deny; deny takes precedence.`},
			{Severity: "error", Code: "network.invalid_proxy", Scope: "network",
				Message: "Proxy configuration is invalid: parse \"https://user:pass@proxy.internal:8080\": bad"},
			{Severity: "warning", Code: "provider.missing_key", Scope: "provider:corp-internal-llm",
				Message: "The configured API key is missing."},
			{Severity: "warning", Code: "derived.invalid_json", Scope: "derived:tabs",
				Message: "Derived desktop state desktop-tabs.json is malformed."},
		},
	}

	payload, err := json.Marshal(providerSafeReportFrom(report))
	if err != nil {
		t.Fatal(err)
	}
	body := string(payload)
	for _, secret := range secrets {
		if strings.Contains(body, secret) {
			t.Fatalf("outbound payload leaked %q:\n%s", secret, body)
		}
	}
	for _, want := range []string{
		`"root":"\u003cproject\u003e"`, // json.Marshal HTML-escapes <project>
		`"code":"config.invalid_toml"`,
		`"scope":"provider"`,
		`"scope":"plugin"`,
		`"scope":"derived:tabs"`,
		`"id":"20260715T000000.000000000Z-abcdef123456"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("outbound payload missing %s:\n%s", want, body)
		}
	}
}

func TestProviderSafeScopeClosedVocabulary(t *testing.T) {
	cases := map[string]string{
		"":                       "",
		"global":                 "global",
		"project":                "project",
		"derived:tabs":           "derived:tabs",
		"derived:zoom":           "derived:zoom",
		"derived:/etc/passwd":    "derived:other",
		"provider:corp-llm":      "provider",
		"plugin:internal-mcp":    "plugin",
		"credential file":        "credential file",
		"someone@example.com":    "other",
		"/Users/someone/project": "other",
	}
	for in, want := range cases {
		if got := providerSafeScope(in); got != want {
			t.Errorf("providerSafeScope(%q) = %q, want %q", in, got, want)
		}
	}
}
