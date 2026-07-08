package secrets

import (
	"strings"
	"testing"
)

func TestRedactMasksCommonSecretShapes(t *testing.T) {
	in := strings.Join([]string{
		"DEEPSEEK_API_KEY=sk-real-secret-value-123456",
		"Authorization: Bearer ghp_abcdefghijklmnopqrstuvwxyz",
		"token xoxb-123456789012-abcdefabcdef",
		"jwt eyJabc.def.ghi",
	}, "\n")

	got := Redact(in)
	for _, leaked := range []string{
		"sk-real-secret-value-123456",
		"ghp_abcdefghijklmnopqrstuvwxyz",
		"xoxb-123456789012-abcdefabcdef",
		"eyJabc.def.ghi",
	} {
		if strings.Contains(got, leaked) {
			t.Fatalf("secret leaked %q in:\n%s", leaked, got)
		}
	}
	for _, want := range []string{"DEEPSEEK_API_KEY=sk-rea", "Authorization: [redacted]"} {
		if !strings.Contains(got, want) {
			t.Fatalf("redacted output missing %q:\n%s", want, got)
		}
	}
}

func TestFilterEnvDropsSensitiveKeys(t *testing.T) {
	got := FilterEnv([]string{
		"PATH=/usr/bin",
		"DEEPSEEK_API_KEY=sk-real-secret-value-123456",
		"GH_TOKEN=ghp_abcdefghijklmnopqrstuvwxyz",
		"HOME=/tmp/home",
	})
	joined := strings.Join(got, "\n")
	if strings.Contains(joined, "DEEPSEEK_API_KEY") || strings.Contains(joined, "GH_TOKEN") {
		t.Fatalf("sensitive env survived:\n%s", joined)
	}
	if !strings.Contains(joined, "PATH=/usr/bin") || !strings.Contains(joined, "HOME=/tmp/home") {
		t.Fatalf("non-sensitive env dropped:\n%s", joined)
	}
}
