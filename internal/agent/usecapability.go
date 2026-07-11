package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"reasonix/internal/capability"
	"reasonix/internal/config"
	"reasonix/internal/event"
	"reasonix/internal/plugin"
	"reasonix/internal/tool"
)

// UseCapabilityTool is the Delivery-only stable proxy for inspecting, calling,
// or declining catalog capabilities. Call never adds dynamic MCP tools to the
// main registry — subsequent calls keep using this stable schema.
type UseCapabilityTool struct {
	mu sync.Mutex

	host     *plugin.Host
	plugins  []config.PluginEntry
	registry *tool.Registry // live registry for already-exposed MCP tools
	ledger   *capability.Ledger
	audit    *capability.Audit
	catalog  func() capability.Catalog
	// proxyClients holds on-demand connected clients that must not pollute the
	// provider-visible registry. Tools are looked up via host.ToolsFor.
	connected map[string]bool
}

// NewUseCapabilityTool builds the Delivery-only capability proxy.
func NewUseCapabilityTool(host *plugin.Host, plugins []config.PluginEntry, reg *tool.Registry, ledger *capability.Ledger, audit *capability.Audit, catalog func() capability.Catalog) *UseCapabilityTool {
	return &UseCapabilityTool{
		host:      host,
		plugins:   append([]config.PluginEntry(nil), plugins...),
		registry:  reg,
		ledger:    ledger,
		audit:     audit,
		catalog:   catalog,
		connected: map[string]bool{},
	}
}

func (*UseCapabilityTool) Name() string { return "use_capability" }

func (*UseCapabilityTool) Description() string {
	return "Delivery profile capability proxy: inspect Skill/MCP metadata, call MCP tools (including auto_start=false servers) without changing the provider tool schema, or decline a prefer capability with a non-empty reason. Skills still use run_skill; this tool only proxies MCP calls."
}

func (*UseCapabilityTool) ReadOnly() bool { return true }

func (*UseCapabilityTool) Schema() json.RawMessage {
	// Stable schema — must not change across turns or when MCP connects.
	return json.RawMessage(`{
		"type":"object",
		"properties":{
			"action":{"type":"string","description":"inspect | call | decline"},
			"capability_id":{"type":"string","description":"Capability id such as skill:review, mcp-server:github, or mcp-tool:github/search_issues"},
			"arguments":{"type":"object","description":"Raw MCP tool arguments for action=call"},
			"reason":{"type":"string","description":"Required non-empty reason when action=decline"}
		},
		"required":["action","capability_id"]
	}`)
}

// ResolveCall implements tool.CallResolver so the agent can run permission,
// hooks, and evidence against the real MCP target before execution.
func (t *UseCapabilityTool) ResolveCall(ctx context.Context, args json.RawMessage) (tool.ResolvedCall, error) {
	var p struct {
		Action       string          `json:"action"`
		CapabilityID string          `json:"capability_id"`
		Arguments    json.RawMessage `json:"arguments"`
		Reason       string          `json:"reason"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return tool.ResolvedCall{}, fmt.Errorf("invalid args: %w", err)
	}
	action := strings.ToLower(strings.TrimSpace(p.Action))
	id := strings.TrimSpace(p.CapabilityID)
	if id == "" {
		return tool.ResolvedCall{}, fmt.Errorf("capability_id is required")
	}
	base := tool.ResolvedCall{
		DisplayName:  "use_capability",
		ProxyAction:  action,
		CapabilityID: id,
		Args:         p.Arguments,
	}
	switch action {
	case "inspect":
		out, err := t.inspect(ctx, id)
		if err != nil {
			if t.audit != nil {
				t.audit.RecordMCPProxy(true, false, true)
			}
			return tool.ResolvedCall{}, err
		}
		if t.audit != nil {
			t.audit.RecordMCPProxy(true, false, false)
		}
		base.SkipExecute = true
		base.Result = out
		base.ReadOnly = true
		return base, nil
	case "decline":
		reason := strings.TrimSpace(p.Reason)
		if reason == "" {
			return tool.ResolvedCall{}, fmt.Errorf("reason is required for action=decline")
		}
		// Decline must not skip require.
		if t.ledger != nil {
			if e, ok := t.ledger.Get(id); ok && e.Policy == capability.AutoUseRequire {
				return tool.ResolvedCall{}, fmt.Errorf("cannot decline a require capability %q", id)
			}
			if err := t.ledger.MarkDeclined(id, reason); err != nil {
				return tool.ResolvedCall{}, err
			}
		}
		base.SkipExecute = true
		base.Result = fmt.Sprintf("declined capability %s: %s", id, reason)
		base.ReadOnly = true
		return base, nil
	case "call":
		return t.resolveCall(ctx, id, p.Arguments, base)
	default:
		return tool.ResolvedCall{}, fmt.Errorf("unknown action %q; use inspect, call, or decline", p.Action)
	}
}

func (t *UseCapabilityTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	resolved, err := t.ResolveCall(ctx, args)
	if err != nil {
		return "", err
	}
	if resolved.SkipExecute {
		return resolved.Result, nil
	}
	if resolved.Unavailable {
		if t.ledger != nil {
			t.ledger.MarkUnavailable(resolved.CapabilityID, resolved.UnavailableReason)
		}
		return "", fmt.Errorf("capability unavailable: %s", resolved.UnavailableReason)
	}
	if resolved.Target == nil {
		return "", fmt.Errorf("no target tool resolved for %s", resolved.CapabilityID)
	}
	if t.ledger != nil {
		t.ledger.MarkInvoked(resolved.CapabilityID)
	}
	if t.audit != nil {
		t.audit.RecordMCPProxy(false, true, false)
	}
	out, err := resolved.Target.Execute(ctx, resolved.Args)
	if err != nil {
		if t.ledger != nil {
			t.ledger.MarkFailed(resolved.CapabilityID, err.Error())
		}
		if t.audit != nil {
			t.audit.RecordMCPProxy(false, true, true)
		}
		return out, err
	}
	if t.ledger != nil {
		t.ledger.MarkSucceeded(resolved.CapabilityID)
	}
	return out, nil
}

func (t *UseCapabilityTool) inspect(ctx context.Context, id string) (string, error) {
	cat := t.currentCatalog()
	if e, ok := cat.Lookup(id); ok {
		b, _ := json.MarshalIndent(map[string]any{
			"id":          e.ID,
			"kind":        e.Kind,
			"name":        e.Name,
			"description": e.Description,
			"status":      e.Status,
			"read_only":   e.ReadOnly,
			"auto_use":    e.AutoUse,
			"requires":    e.Requires,
			"profiles":    e.Profiles,
			"tool_name":   e.ToolName,
			"auto_start":  e.AutoStart,
		}, "", "  ")
		// For MCP without ready tools, attempt a restricted handshake to list tools.
		if e.Kind == capability.KindMCPServer || e.Kind == capability.KindMCPTool {
			server := e.Source
			if server == "" {
				server = e.ConnectName
			}
			if server != "" {
				tools, err := t.ensureServerTools(ctx, server)
				if err != nil {
					return string(b) + "\n\nHandshake failed: " + err.Error(), nil
				}
				type toolInfo struct {
					ID          string          `json:"id"`
					Name        string          `json:"name"`
					Description string          `json:"description"`
					ReadOnly    bool            `json:"read_only"`
					Schema      json.RawMessage `json:"input_schema,omitempty"`
				}
				var list []toolInfo
				for _, tl := range tools {
					raw := ""
					if m, ok := tl.(tool.MCPMetadata); ok {
						raw = m.MCPRawToolName()
					}
					list = append(list, toolInfo{
						ID:          "mcp-tool:" + server + "/" + raw,
						Name:        tl.Name(),
						Description: tl.Description(),
						ReadOnly:    tl.ReadOnly(),
						Schema:      tl.Schema(),
					})
				}
				extra, _ := json.MarshalIndent(list, "", "  ")
				return string(b) + "\n\nTools:\n" + string(extra), nil
			}
		}
		return string(b), nil
	}
	return "", fmt.Errorf("unknown capability_id %q", id)
}

func (t *UseCapabilityTool) resolveCall(ctx context.Context, id string, args json.RawMessage, base tool.ResolvedCall) (tool.ResolvedCall, error) {
	server, raw, err := parseMCPCapabilityID(id)
	if err != nil {
		// Skills must use run_skill.
		if strings.HasPrefix(id, "skill:") {
			return tool.ResolvedCall{}, fmt.Errorf("call only proxies MCP tools; use run_skill for %s", id)
		}
		return tool.ResolvedCall{}, err
	}
	// Prefer already-exposed registry tool (auto-started MCP).
	modelName := plugin.ToolPrefix(server) + normalizeToolToken(raw)
	if t.registry != nil {
		if tl, ok := t.registry.Get(modelName); ok {
			base.TargetName = modelName
			base.Target = tl
			base.ReadOnly = tl.ReadOnly()
			if len(args) == 0 {
				base.Args = json.RawMessage(`{}`)
			} else {
				base.Args = args
			}
			return base, nil
		}
		// Also try exact raw-normalized scan.
		for _, name := range t.registry.Names() {
			s, r, ok := tool.SplitMCPName(name)
			if ok && s == normalizeToolToken(server) && (r == normalizeToolToken(raw) || r == raw) {
				tl, _ := t.registry.Get(name)
				base.TargetName = name
				base.Target = tl
				base.ReadOnly = tl.ReadOnly()
				if len(args) == 0 {
					base.Args = json.RawMessage(`{}`)
				} else {
					base.Args = args
				}
				return base, nil
			}
		}
	}
	tools, err := t.ensureServerTools(ctx, server)
	if err != nil {
		base.Unavailable = true
		base.UnavailableReason = err.Error()
		base.SkipExecute = true
		base.Result = "capability unavailable: " + err.Error()
		base.TargetName = modelName
		// Conservative: uncached tools are writers until handshake succeeds.
		base.ReadOnly = false
		if t.ledger != nil {
			t.ledger.MarkUnavailable(id, err.Error())
		}
		if t.audit != nil {
			t.audit.RecordMCPProxy(false, true, true)
		}
		return base, nil
	}
	var target tool.Tool
	for _, tl := range tools {
		if m, ok := tl.(tool.MCPMetadata); ok {
			if m.MCPRawToolName() == raw || normalizeToolToken(m.MCPRawToolName()) == normalizeToolToken(raw) {
				target = tl
				base.TargetName = tl.Name()
				break
			}
		}
		if tl.Name() == modelName {
			target = tl
			base.TargetName = modelName
			break
		}
	}
	if target == nil {
		msg := fmt.Sprintf("MCP tool %q not found on server %q", raw, server)
		base.Unavailable = true
		base.UnavailableReason = msg
		base.SkipExecute = true
		base.Result = "capability unavailable: " + msg
		base.TargetName = modelName
		base.ReadOnly = false
		if t.ledger != nil {
			t.ledger.MarkUnavailable(id, msg)
		}
		return base, nil
	}
	base.Target = target
	base.ReadOnly = target.ReadOnly()
	if len(args) == 0 {
		base.Args = json.RawMessage(`{}`)
	} else {
		base.Args = args
	}
	return base, nil
}

func (t *UseCapabilityTool) ensureServerTools(ctx context.Context, server string) ([]tool.Tool, error) {
	server = strings.TrimSpace(server)
	if server == "" {
		return nil, fmt.Errorf("empty MCP server name")
	}
	if t.host == nil {
		return nil, fmt.Errorf("MCP host unavailable")
	}
	// Reuse shared host if already connected (including auto-started).
	if t.host.HasClient(server) {
		return t.host.ToolsFor(ctx, server)
	}
	spec, ok := t.specFor(server)
	if !ok {
		return nil, fmt.Errorf("MCP server %q is not configured", server)
	}
	// On-demand connect with a short startup budget; tools stay off main registry.
	startCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tools, err := t.host.Add(startCtx, spec)
	if err != nil {
		if plugin.IsServerAlreadyConnected(err) {
			return t.host.ToolsFor(ctx, server)
		}
		t.host.RecordFailure(spec, err)
		return nil, fmt.Errorf("connect %q: %w", server, err)
	}
	t.mu.Lock()
	t.connected[server] = true
	t.mu.Unlock()
	// Intentionally do NOT add tools to t.registry — Delivery schema stays stable.
	_ = tools
	return t.host.ToolsFor(ctx, server)
}

func (t *UseCapabilityTool) specFor(server string) (plugin.Spec, bool) {
	for _, p := range t.plugins {
		if p.Name == server {
			return plugin.Spec{
				Name:    p.Name,
				Type:    p.Type,
				Command: p.Command,
				Args:    append([]string(nil), p.Args...),
				Env:     p.Env,
				URL:     p.URL,
				Headers: p.Headers,
			}, true
		}
	}
	return plugin.Spec{}, false
}

func (t *UseCapabilityTool) currentCatalog() capability.Catalog {
	if t.catalog != nil {
		return t.catalog()
	}
	return capability.Catalog{}
}

func parseMCPCapabilityID(id string) (server, raw string, err error) {
	id = strings.TrimSpace(id)
	switch {
	case strings.HasPrefix(id, "mcp-tool:"):
		rest := strings.TrimPrefix(id, "mcp-tool:")
		server, raw, ok := strings.Cut(rest, "/")
		if !ok || server == "" || raw == "" {
			return "", "", fmt.Errorf("invalid mcp-tool id %q; want mcp-tool:<server>/<tool>", id)
		}
		return server, raw, nil
	case strings.HasPrefix(id, "mcp-server:"):
		return "", "", fmt.Errorf("action=call requires mcp-tool:<server>/<tool>, not %q", id)
	default:
		return "", "", fmt.Errorf("action=call requires an mcp-tool capability id, got %q", id)
	}
}

func normalizeToolToken(s string) string {
	// Match plugin.normalizeName loosely: keep alnum underscore hyphen.
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// Ensure UseCapabilityTool satisfies the tool contracts used by the agent.
var (
	_ tool.Tool         = (*UseCapabilityTool)(nil)
	_ tool.CallResolver = (*UseCapabilityTool)(nil)
)

// EmitProxyAudit is a helper for frontends: returns a notice describing the
// proxy name and real target for user audit trails.
func EmitProxyAudit(sink event.Sink, resolved tool.ResolvedCall) {
	if sink == nil || resolved.TargetName == "" {
		return
	}
	sink.Emit(event.Event{
		Kind:   event.Notice,
		Level:  event.LevelInfo,
		Text:   fmt.Sprintf("capability proxy: %s → %s", resolved.DisplayName, resolved.TargetName),
		Detail: resolved.CapabilityID,
	})
}
