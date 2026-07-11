package provider

import (
	"encoding/json"
	"net/url"
	"strings"
)

// IsMiMoEndpoint reports whether rawURL points at an official Xiaomi MiMo API
// host, including the regional token-plan subdomains. The bare apex is rejected
// because it is not an API endpoint.
func IsMiMoEndpoint(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host != "xiaomimimo.com" && strings.HasSuffix(host, ".xiaomimimo.com")
}

// NormalizeLegacyTupleItemsForDraft202012 rewrites only the pre-2020-12 tuple
// keywords in a JSON Schema. It is intentionally separate from
// CanonicalizeSchema: provider implementations must opt in only after the target
// endpoint's schema dialect is known, so other vendors keep their original tool
// schema bytes and cache prefixes.
func NormalizeLegacyTupleItemsForDraft202012(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	var schema any
	if err := json.Unmarshal(raw, &schema); err != nil {
		return raw
	}
	normalizeDraft202012Schema(schema)
	out, err := json.Marshal(schema)
	if err != nil {
		return raw
	}
	return json.RawMessage(out)
}

func normalizeDraft202012Schema(value any) {
	schema, ok := value.(map[string]any)
	if !ok {
		return
	}

	for _, keyword := range []string{
		"additionalItems", "additionalProperties", "contains", "contentSchema",
		"else", "if", "items", "not", "propertyNames", "then",
		"unevaluatedItems", "unevaluatedProperties",
	} {
		normalizeDraft202012Schema(schema[keyword])
	}
	for _, keyword := range []string{"allOf", "anyOf", "oneOf", "prefixItems"} {
		if children, ok := schema[keyword].([]any); ok {
			for _, child := range children {
				normalizeDraft202012Schema(child)
			}
		}
	}
	for _, keyword := range []string{
		"$defs", "definitions", "dependentSchemas", "patternProperties", "properties",
	} {
		if children, ok := schema[keyword].(map[string]any); ok {
			for _, child := range children {
				normalizeDraft202012Schema(child)
			}
		}
	}
	if dependencies, ok := schema["dependencies"].(map[string]any); ok {
		for _, child := range dependencies {
			normalizeDraft202012Schema(child)
		}
	}

	legacyItems, ok := schema["items"].([]any)
	if !ok {
		return
	}
	for _, child := range legacyItems {
		normalizeDraft202012Schema(child)
	}

	delete(schema, "items")
	if len(legacyItems) > 0 {
		// Keep an explicit 2020-12 prefix if a malformed mixed-dialect schema
		// contains both forms.
		if _, exists := schema["prefixItems"]; !exists {
			schema["prefixItems"] = legacyItems
		}
	}
	if additional, exists := schema["additionalItems"]; exists {
		delete(schema, "additionalItems")
		if isSchemaObjectOrBool(additional) {
			schema["items"] = additional
		}
	}
}

func isSchemaObjectOrBool(value any) bool {
	switch value.(type) {
	case map[string]any, bool:
		return true
	default:
		return false
	}
}
