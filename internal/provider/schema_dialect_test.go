package provider

import (
	"encoding/json"
	"testing"
)

func TestIsMiMoEndpoint(t *testing.T) {
	for _, tc := range []struct {
		url  string
		want bool
	}{
		{"https://api.xiaomimimo.com/v1", true},
		{"https://api.xiaomimimo.com/anthropic", true},
		{"https://token-plan-cn.xiaomimimo.com/v1", true},
		{"https://token-plan-sgp.xiaomimimo.com/anthropic", true},
		{"https://token-plan-ams.xiaomimimo.com/v1", true},
		{"https://xiaomimimo.com/v1", false},
		{"https://api.deepseek.com", false},
		{"https://xiaomimimo.com.example.org", false},
		{"", false},
		{"not-a-url", false},
	} {
		if got := IsMiMoEndpoint(tc.url); got != tc.want {
			t.Errorf("IsMiMoEndpoint(%q) = %v, want %v", tc.url, got, tc.want)
		}
	}
}

func TestNormalizeLegacyTupleItemsDoesNotRewriteSchemaExamples(t *testing.T) {
	raw := json.RawMessage(`{
		"type":"object",
		"properties":{
			"value":{
				"type":"object",
				"default":{"items":["first","second"]}
			}
		}
	}`)
	got := NormalizeLegacyTupleItemsForDraft202012(raw)
	var schema map[string]any
	if err := json.Unmarshal(got, &schema); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	properties := schema["properties"].(map[string]any)
	value := properties["value"].(map[string]any)
	defaultValue := value["default"].(map[string]any)
	if _, ok := defaultValue["items"].([]any); !ok {
		t.Fatalf("schema default was rewritten: %s", got)
	}
	if _, exists := defaultValue["prefixItems"]; exists {
		t.Fatalf("schema default gained prefixItems: %s", got)
	}
}
