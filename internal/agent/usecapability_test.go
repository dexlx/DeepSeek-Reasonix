package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/capability"
	"reasonix/internal/tool"
)

type denyAllGate struct{}

func (denyAllGate) Check(_ context.Context, name string, _ json.RawMessage, _ bool) (bool, string, error) {
	return false, "denied " + name, nil
}

func TestUseCapabilityDeclineAndInspect(t *testing.T) {
	ledger := capability.NewLedger()
	ledger.SeedCandidates(capability.RouteDecision{Candidates: []capability.RouteCandidate{
		{Entry: capability.Entry{ID: "skill:review"}, Policy: capability.AutoUsePrefer},
	}})
	tl := NewUseCapabilityTool(nil, nil, tool.NewRegistry(), ledger, nil, func() capability.Catalog {
		return capability.Catalog{Entries: []capability.Entry{{
			ID: "skill:review", Kind: capability.KindSkill, Name: "review", Description: "review code", Status: capability.StatusReady,
		}}}
	})

	out, err := tl.Execute(context.Background(), json.RawMessage(`{"action":"inspect","capability_id":"skill:review"}`))
	if err != nil || !strings.Contains(out, "skill:review") {
		t.Fatalf("inspect: out=%q err=%v", out, err)
	}
	if _, err := tl.Execute(context.Background(), json.RawMessage(`{"action":"decline","capability_id":"skill:review","reason":"not needed"}`)); err != nil {
		t.Fatal(err)
	}
	if gate := ledger.CheckFinalGate(); gate.Reason != "" {
		t.Fatalf("after decline gate = %+v", gate)
	}
	// Cannot decline require.
	ledger.SeedCandidates(capability.RouteDecision{Candidates: []capability.RouteCandidate{
		{Entry: capability.Entry{ID: "skill:must"}, Policy: capability.AutoUseRequire},
	}})
	if _, err := tl.Execute(context.Background(), json.RawMessage(`{"action":"decline","capability_id":"skill:must","reason":"no"}`)); err == nil {
		t.Fatal("expected decline of require to fail")
	}
}

func TestUseCapabilityProxyHonorsRealMCPPermissionDeny(t *testing.T) {
	// Register a fake MCP tool in the registry so resolve uses it without host.
	reg := tool.NewRegistry()
	reg.Add(fakeTool{name: "mcp__github__search_issues", readOnly: true})
	tl := NewUseCapabilityTool(nil, nil, reg, capability.NewLedger(), nil, nil)

	resolved, err := tl.ResolveCall(context.Background(), json.RawMessage(`{"action":"call","capability_id":"mcp-tool:github/search_issues","arguments":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	if resolved.TargetName != "mcp__github__search_issues" {
		t.Fatalf("target = %q", resolved.TargetName)
	}
	if resolved.Target == nil {
		t.Fatal("expected resolved target tool")
	}
	gate := denyAllGate{}
	allow, reason, _ := gate.Check(context.Background(), resolved.TargetName, resolved.Args, resolved.ReadOnly)
	if allow || !strings.Contains(reason, "mcp__github__search_issues") {
		t.Fatalf("gate allow=%v reason=%q", allow, reason)
	}
}

func TestReviewReportToolValidatesSchema(t *testing.T) {
	tl := NewReviewReportTool()
	if _, err := tl.Execute(context.Background(), json.RawMessage(`{"kind":"review","verdict":"pass","reviewed_paths":[]}`)); err == nil {
		t.Fatal("empty reviewed_paths should fail")
	}
	out, err := tl.Execute(context.Background(), json.RawMessage(`{"kind":"security","verdict":"block","reviewed_paths":["a.go"],"findings":[{"severity":"critical","summary":"secret"}]}`))
	if err != nil || !strings.Contains(out, "blocking") {
		t.Fatalf("out=%q err=%v", out, err)
	}
}
