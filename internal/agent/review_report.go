package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"reasonix/internal/evidence"
	"reasonix/internal/tool"
)

// ReviewReportTool is visible only inside review/security_review subagent
// registries. It submits a structured review result the host uses for
// Delivery risk gates. It is never registered on the parent agent tool surface.
type ReviewReportTool struct{}

func NewReviewReportTool() *ReviewReportTool { return &ReviewReportTool{} }

func (*ReviewReportTool) Name() string { return "review_report" }

func (*ReviewReportTool) Description() string {
	return "Submit a structured review result for the parent delivery gate. Call once when the review is complete. kind is review or security; verdict is pass, warn, or block; reviewed_paths must cover the production paths you inspected; findings list severity/summary/path/line."
}

func (*ReviewReportTool) ReadOnly() bool { return true }

func (*ReviewReportTool) Schema() json.RawMessage {
	// Fixed schema — stable for review subagents only.
	return json.RawMessage(`{
		"type":"object",
		"properties":{
			"kind":{"type":"string","description":"review | security"},
			"verdict":{"type":"string","description":"pass | warn | block"},
			"reviewed_paths":{"type":"array","items":{"type":"string"},"description":"Production paths covered by this review"},
			"findings":{"type":"array","items":{"type":"object","properties":{
				"severity":{"type":"string"},
				"summary":{"type":"string"},
				"path":{"type":"string"},
				"line":{"type":"integer"}
			},"required":["severity","summary"]}}
		},
		"required":["kind","verdict","reviewed_paths"]
	}`)
}

func (*ReviewReportTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	report, err := evidence.ParseReviewReport(args)
	if err != nil {
		return "", err
	}
	// Evidence is recorded by the agent host from the tool call args; this
	// result is a human-readable confirmation for the subagent transcript.
	msg := fmt.Sprintf("review_report accepted: kind=%s verdict=%s paths=%d findings=%d",
		report.Kind, report.Verdict, len(report.ReviewedPaths), len(report.Findings))
	if report.HasBlockingFinding() {
		msg += " (blocking — parent delivery will require fixes and re-review)"
	}
	return msg, nil
}

var _ tool.Tool = (*ReviewReportTool)(nil)

// AttachReviewReportTool adds review_report to a subagent registry used by
// review / security_review skills only.
func AttachReviewReportTool(reg *tool.Registry) {
	if reg == nil {
		return
	}
	reg.Add(NewReviewReportTool())
}
