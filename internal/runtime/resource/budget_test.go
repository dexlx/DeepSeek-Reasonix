package resource

import (
	"strings"
	"testing"
)

func TestEnforceBlocksExceededBudgets(t *testing.T) {
	decision := Enforce(ResourceBudget{MaxTokens: 10, MaxToolCalls: 1, MaxMemoryNodes: 2}, Usage{
		Tokens:      11,
		ToolCalls:   2,
		MemoryNodes: 3,
	})
	if decision.Allowed {
		t.Fatalf("decision allowed exceeded usage: %+v", decision)
	}
	got := strings.Join(decision.Reasons, "\n")
	for _, want := range []string{"token budget exceeded", "tool call budget exceeded", "memory node budget exceeded"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in reasons: %+v", want, decision.Reasons)
		}
	}
}

func TestScaleForCanaryKeepsMinimumBudget(t *testing.T) {
	scaled := ScaleForCanary(ResourceBudget{MaxTokens: 5, MaxToolCalls: 5, MaxMemoryNodes: 10}, 1)
	if scaled.MaxTokens != 1 || scaled.MaxToolCalls != 1 || scaled.MaxMemoryNodes != 10 {
		t.Fatalf("scaled budget = %+v, want bounded token/tool budgets and unchanged memory cap", scaled)
	}
}

func TestReservationCommitBlocksUnreservedAsyncUsage(t *testing.T) {
	reservation := Reserve(ResourceBudget{MaxTokens: 100, MaxToolCalls: 2, MaxMemoryNodes: 10}, Usage{
		Tokens:      50,
		ToolCalls:   1,
		MemoryNodes: 5,
	})
	if !reservation.Allowed {
		t.Fatalf("reservation rejected valid usage: %+v", reservation)
	}
	decision := reservation.Commit(Usage{Tokens: 60, ToolCalls: 2, MemoryNodes: 5})
	if decision.Allowed {
		t.Fatalf("commit allowed unreserved async usage: %+v", decision)
	}
	got := strings.Join(decision.Reasons, "\n")
	for _, want := range []string{"unreserved token usage", "unreserved tool call usage"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in reasons: %+v", want, decision.Reasons)
		}
	}
}
