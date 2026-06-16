package suite

import (
	"fmt"
	"testing"
)

// Marker prefixes. These are emitted through t.Log so they appear in the
// `go test -json` Output stream, where the HTML report generator
// (cmd/report-gen) parses them into structured sections. They are also
// perfectly readable in a plain `go test -v` run.
const (
	markerDesc   = "⟫DESC⟫ "
	markerStep   = "⟫STEP⟫ "
	markerMetric = "⟫METRIC⟫ "
	markerInfo   = "⟫INFO⟫ "
	markerCheck  = "⟫CHECK⟫ "
)

// Reporter wraps *testing.T to emit both human-readable logs and
// machine-parseable markers for the HTML report. Every test case creates one.
type Reporter struct {
	t    *testing.T
	step int
}

// NewReporter starts a reporter and records the case description.
func NewReporter(t *testing.T, description string) *Reporter {
	t.Helper()
	t.Log(markerDesc + description)
	return &Reporter{t: t}
}

// Step records a numbered step in the scenario timeline.
func (r *Reporter) Step(format string, args ...any) {
	r.t.Helper()
	r.step++
	msg := fmt.Sprintf(format, args...)
	r.t.Logf("%s[%d] %s", markerStep, r.step, msg)
}

// Info records a non-step informational line.
func (r *Reporter) Info(format string, args ...any) {
	r.t.Helper()
	r.t.Logf("%s%s", markerInfo, fmt.Sprintf(format, args...))
}

// Metric records a named measurement (rendered as a metric tile in the report).
// unit is optional (pass "" to omit).
func (r *Reporter) Metric(name string, value any, unit string) {
	r.t.Helper()
	r.t.Logf("%s%s=%v|%s", markerMetric, name, value, unit)
}

// Check records an assertion outcome with a label. ok=false also fails the test.
func (r *Reporter) Check(ok bool, format string, args ...any) bool {
	r.t.Helper()
	label := fmt.Sprintf(format, args...)
	status := "PASS"
	if !ok {
		status = "FAIL"
	}
	r.t.Logf("%s%s | %s", markerCheck, status, label)
	if !ok {
		r.t.Errorf("check failed: %s", label)
	}
	return ok
}

// Fatalf records a fatal failure and stops the test.
func (r *Reporter) Fatalf(format string, args ...any) {
	r.t.Helper()
	r.t.Fatalf(format, args...)
}
