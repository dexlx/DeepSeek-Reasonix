package evidence

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ReviewKind distinguishes ordinary review from security review reports.
type ReviewKind string

const (
	ReviewKindReview   ReviewKind = "review"
	ReviewKindSecurity ReviewKind = "security"
)

// ReviewVerdict is the structured outcome of a review sub-agent.
type ReviewVerdict string

const (
	ReviewVerdictPass  ReviewVerdict = "pass"
	ReviewVerdictWarn  ReviewVerdict = "warn"
	ReviewVerdictBlock ReviewVerdict = "block"
)

// ReviewFinding is one structured finding inside a review_report.
type ReviewFinding struct {
	Severity string `json:"severity"`
	Summary  string `json:"summary"`
	Path     string `json:"path,omitempty"`
	Line     int    `json:"line,omitempty"`
}

// ReviewReport is the structured payload submitted via the review_report tool.
type ReviewReport struct {
	Kind          ReviewKind      `json:"kind"`
	Verdict       ReviewVerdict   `json:"verdict"`
	ReviewedPaths []string        `json:"reviewed_paths"`
	Findings      []ReviewFinding `json:"findings"`
}

// ParseReviewReport validates and normalizes a review_report argument object.
func ParseReviewReport(raw json.RawMessage) (ReviewReport, error) {
	var r ReviewReport
	if err := json.Unmarshal(raw, &r); err != nil {
		return ReviewReport{}, fmt.Errorf("invalid review_report JSON: %w", err)
	}
	r.Kind = ReviewKind(strings.ToLower(strings.TrimSpace(string(r.Kind))))
	r.Verdict = ReviewVerdict(strings.ToLower(strings.TrimSpace(string(r.Verdict))))
	switch r.Kind {
	case ReviewKindReview, ReviewKindSecurity:
	default:
		return ReviewReport{}, fmt.Errorf("review_report.kind must be review or security")
	}
	switch r.Verdict {
	case ReviewVerdictPass, ReviewVerdictWarn, ReviewVerdictBlock:
	default:
		return ReviewReport{}, fmt.Errorf("review_report.verdict must be pass, warn, or block")
	}
	r.ReviewedPaths = normalizePaths(r.ReviewedPaths)
	if len(r.ReviewedPaths) == 0 {
		return ReviewReport{}, fmt.Errorf("review_report.reviewed_paths must be non-empty")
	}
	clean := make([]ReviewFinding, 0, len(r.Findings))
	for _, f := range r.Findings {
		f.Severity = strings.TrimSpace(f.Severity)
		f.Summary = strings.TrimSpace(f.Summary)
		f.Path = strings.TrimSpace(f.Path)
		if f.Summary == "" {
			return ReviewReport{}, fmt.Errorf("review_report.findings require a non-empty summary")
		}
		if f.Severity == "" {
			f.Severity = "info"
		}
		clean = append(clean, f)
	}
	r.Findings = clean
	return r, nil
}

// CoversPaths reports whether every required production path was reviewed.
func (r ReviewReport) CoversPaths(required []string) bool {
	if len(required) == 0 {
		return len(r.ReviewedPaths) > 0
	}
	have := pathSet(normalizePaths(r.ReviewedPaths))
	for _, p := range normalizePaths(required) {
		if p == "" {
			continue
		}
		if !have[p] {
			// Also accept basename coverage for short relative refs.
			found := false
			base := filepathBase(p)
			for h := range have {
				if h == p || filepathBase(h) == base {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
	}
	return true
}

// HasBlockingFinding reports whether the verdict forbids delivery.
func (r ReviewReport) HasBlockingFinding() bool {
	if r.Verdict == ReviewVerdictBlock {
		return true
	}
	for _, f := range r.Findings {
		switch strings.ToLower(f.Severity) {
		case "block", "blocking", "critical", "error":
			return true
		}
	}
	return false
}

// WarningSummaries returns human-readable warn-level findings for the final summary.
func (r ReviewReport) WarningSummaries() []string {
	var out []string
	if r.Verdict == ReviewVerdictWarn {
		out = append(out, "review verdict=warn")
	}
	for _, f := range r.Findings {
		switch strings.ToLower(f.Severity) {
		case "warn", "warning", "medium":
			msg := f.Summary
			if f.Path != "" {
				msg = f.Path + ": " + msg
			}
			out = append(out, msg)
		}
	}
	return out
}

func filepathBase(p string) string {
	p = strings.ReplaceAll(p, `\`, `/`)
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// ReviewReportReceipt is stored on the ledger when a review_report succeeds.
type ReviewReportReceipt struct {
	Report ReviewReport
	After  int // mutation index this report claims to cover; -1 if unknown
}

// HasStructuredReviewAfter reports whether a successful structured review of
// the given kind was recorded after the mutation, covering required paths, and
// without a blocking verdict.
func (l *Ledger) HasStructuredReviewAfter(kind ReviewKind, after int, requiredPaths []string) (ok bool, blocking bool, report *ReviewReport) {
	if l == nil {
		return false, false, nil
	}
	start := after + 1
	if start < 0 {
		start = 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for i := start; i < len(l.receipts); i++ {
		r := l.receipts[i]
		if !r.Success || r.ToolName != "review_report" {
			continue
		}
		parsed, err := ParseReviewReport(r.Args)
		if err != nil {
			continue
		}
		if parsed.Kind != kind {
			continue
		}
		if !parsed.CoversPaths(requiredPaths) {
			continue
		}
		if parsed.HasBlockingFinding() {
			return true, true, &parsed
		}
		return true, false, &parsed
	}
	return false, false, nil
}

// HasSuccessfulStructuredReviewAfter is a convenience for non-blocking coverage.
func (l *Ledger) HasSuccessfulStructuredReviewAfter(kind ReviewKind, after int, requiredPaths []string) bool {
	ok, blocking, _ := l.HasStructuredReviewAfter(kind, after, requiredPaths)
	return ok && !blocking
}
