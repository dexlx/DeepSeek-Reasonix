package secrets

import (
	"os"
	"regexp"
	"strings"

	"reasonix/internal/provider"
)

var (
	secretKeyNamePattern = regexp.MustCompile(`(?i)(^|[_-])(api[_-]?key|access[_-]?key|private[_-]?key|secret|token|password|passwd|pwd)([_-]|$)`)
	keyValuePattern      = regexp.MustCompile(`(?i)\b([A-Z0-9_.-]*(?:API[_-]?KEY|ACCESS[_-]?KEY|PRIVATE[_-]?KEY|SECRET|TOKEN|PASSWORD|PASSWD|PWD)[A-Z0-9_.-]*|authorization)\b(\s*[:=]\s*)(?:Bearer\s+)?(['"]?)([^'"\s,;]+)(['"]?)`)
	bearerTokenPattern   = regexp.MustCompile(`(?i)\bBearer\s+([A-Za-z0-9._~+/=-]{16,})`)
	openAIKeyPattern     = regexp.MustCompile(`\b((?:sk|rk)-(?:proj-)?[A-Za-z0-9_-]{12,})\b`)
	githubTokenPattern   = regexp.MustCompile(`\b(gh[pousr]_[A-Za-z0-9_]{20,}|github_pat_[A-Za-z0-9_]{20,})\b`)
	slackTokenPattern    = regexp.MustCompile(`\b(xox[baprs]-[A-Za-z0-9-]{16,})\b`)
	awsAccessKeyPattern  = regexp.MustCompile(`\b(AKIA[0-9A-Z]{16}|ASIA[0-9A-Z]{16})\b`)
	jwtPattern           = regexp.MustCompile(`\b(eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+)\b`)
)

const redactedValue = "[redacted]"

// EnvKeySensitive reports whether an environment variable name is likely to
// carry credentials. It intentionally keys off the name, not the value, so child
// processes do not inherit saved provider secrets by default.
func EnvKeySensitive(key string) bool {
	key = strings.TrimSpace(key)
	if key == "" {
		return false
	}
	return secretKeyNamePattern.MatchString(key)
}

// FilterEnv removes sensitive KEY=value assignments from an environment vector.
func FilterEnv(env []string) []string {
	out := env[:0]
	for _, item := range env {
		key, _, ok := strings.Cut(item, "=")
		if !ok || EnvKeySensitive(key) {
			continue
		}
		out = append(out, item)
	}
	return out
}

// ProcessEnv returns the current process environment with credential-like
// assignments removed, suitable for shell/tool subprocesses.
func ProcessEnv() []string {
	return FilterEnv(os.Environ())
}

// Redact masks credential-like values in text before the text enters model
// context, UI events, durable transcripts, or diagnostic records.
func Redact(s string) string {
	if s == "" {
		return s
	}
	s = keyValuePattern.ReplaceAllStringFunc(s, func(match string) string {
		parts := keyValuePattern.FindStringSubmatch(match)
		if len(parts) != 6 {
			return redactedValue
		}
		key := parts[1]
		sep := parts[2]
		quote := parts[3]
		value := parts[4]
		endQuote := parts[5]
		if strings.EqualFold(key, "authorization") {
			return key + sep + quote + redactedValue + endQuote
		}
		return key + sep + quote + mask(value) + endQuote
	})
	s = bearerTokenPattern.ReplaceAllStringFunc(s, func(match string) string {
		token := strings.TrimSpace(strings.TrimPrefix(match, "Bearer"))
		if len(token) == len(match) {
			return "Bearer " + redactedValue
		}
		return "Bearer " + mask(token)
	})
	for _, rx := range []*regexp.Regexp{openAIKeyPattern, githubTokenPattern, slackTokenPattern, awsAccessKeyPattern, jwtPattern} {
		s = rx.ReplaceAllStringFunc(s, mask)
	}
	return s
}

func mask(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return redactedValue
	}
	if len(value) <= 12 {
		return redactedValue
	}
	head := 4
	tail := 4
	if strings.HasPrefix(value, "sk-") || strings.HasPrefix(value, "rk-") {
		head = 6
	}
	if len(value) <= head+tail {
		return redactedValue
	}
	return value[:head] + strings.Repeat("*", len(value)-head-tail) + value[len(value)-tail:]
}

// RedactMessage returns a storage/model-safe copy of m with textual secret
// surfaces masked. Images are left untouched because they are opaque data URLs.
func RedactMessage(m provider.Message) provider.Message {
	m.Content = Redact(m.Content)
	m.ReasoningContent = Redact(m.ReasoningContent)
	m.Original = Redact(m.Original)
	for i := range m.ToolCalls {
		m.ToolCalls[i].Arguments = Redact(m.ToolCalls[i].Arguments)
		m.ToolCalls[i].Diff = Redact(m.ToolCalls[i].Diff)
	}
	for i := range m.MemoryCitations {
		m.MemoryCitations[i].Note = Redact(m.MemoryCitations[i].Note)
	}
	return m
}

// RedactMessages returns a redacted copy of msgs.
func RedactMessages(msgs []provider.Message) []provider.Message {
	out := make([]provider.Message, len(msgs))
	for i, m := range msgs {
		out[i] = RedactMessage(m)
	}
	return out
}
