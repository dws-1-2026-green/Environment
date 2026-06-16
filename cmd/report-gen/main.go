// Command report-gen turns `go test -json` output into a single self-contained
// HTML report tailored for the functional test cases in tests/suite.
//
// Usage:
//
//	go test -json ./tests/suite/... | go run ./cmd/report-gen -out report.html
//	# or from a saved file:
//	go run ./cmd/report-gen -in results.json -out report.html
//
// It understands the ⟫DESC⟫ / ⟫STEP⟫ / ⟫METRIC⟫ / ⟫INFO⟫ / ⟫CHECK⟫ markers
// emitted by tests/suite/reporter.go and renders them as structured sections.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"os"
	"sort"
	"strings"
	"time"
)

// testEvent matches the JSON objects emitted by `go test -json`.
type testEvent struct {
	Time    time.Time `json:"Time"`
	Action  string    `json:"Action"`
	Package string    `json:"Package"`
	Test    string    `json:"Test"`
	Elapsed float64   `json:"Elapsed"`
	Output  string    `json:"Output"`
}

type metric struct {
	Name  string
	Value string
	Unit  string
}

type check struct {
	OK    bool
	Label string
}

type testCase struct {
	Name        string
	Status      string // pass | fail | skip
	Elapsed     float64
	Description string
	Steps       []string
	Infos       []string
	Metrics     []metric
	Checks      []check
	RawOutput   []string
}

type report struct {
	Title     string
	Generated string
	Target    string
	Cases     []*testCase
	Total     int
	Passed    int
	Failed    int
	Skipped   int
	Duration  float64
}

const (
	mDesc   = "⟫DESC⟫ "
	mStep   = "⟫STEP⟫ "
	mMetric = "⟫METRIC⟫ "
	mInfo   = "⟫INFO⟫ "
	mCheck  = "⟫CHECK⟫ "
)

func main() {
	inPath := flag.String("in", "", "path to go test -json output (default: stdin)")
	outPath := flag.String("out", "report.html", `path to write the report ("-" = stdout)`)
	format := flag.String("format", "html", "report format: html | text")
	title := flag.String("title", "Функциональные тесты — система доставки вебхуков", "report title")
	target := flag.String("target", os.Getenv("E2E_EVENT_RECEIVER_URL"), "target under test (shown in header)")
	flag.Parse()

	var in *os.File
	if *inPath == "" {
		in = os.Stdin
	} else {
		f, err := os.Open(*inPath)
		if err != nil {
			fatal("open input: %v", err)
		}
		defer f.Close()
		in = f
	}

	cases := map[string]*testCase{}
	var order []string

	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var ev testEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		if ev.Test == "" {
			continue // package-level events
		}
		tc, ok := cases[ev.Test]
		if !ok {
			tc = &testCase{Name: ev.Test, Status: "run"}
			cases[ev.Test] = tc
			order = append(order, ev.Test)
		}
		switch ev.Action {
		case "pass":
			tc.Status = "pass"
			tc.Elapsed = ev.Elapsed
		case "fail":
			tc.Status = "fail"
			tc.Elapsed = ev.Elapsed
		case "skip":
			tc.Status = "skip"
			tc.Elapsed = ev.Elapsed
		case "output":
			parseOutput(tc, ev.Output)
		}
	}
	if err := sc.Err(); err != nil {
		fatal("read input: %v", err)
	}

	rep := &report{
		Title:     *title,
		Generated: time.Now().Format("2006-01-02 15:04:05 MST"),
		Target:    *target,
	}
	// Keep only top-level tests (skip subtests noise is fine to include too).
	for _, name := range order {
		tc := cases[name]
		// Skip the implicit nothing — include all real cases.
		rep.Cases = append(rep.Cases, tc)
		rep.Total++
		rep.Duration += tc.Elapsed
		switch tc.Status {
		case "pass":
			rep.Passed++
		case "fail":
			rep.Failed++
		case "skip":
			rep.Skipped++
		}
	}
	sort.SliceStable(rep.Cases, func(i, j int) bool {
		return rep.Cases[i].Name < rep.Cases[j].Name
	})

	// Output destination: "-" means stdout (e.g. into k8s pod logs).
	out := os.Stdout
	toStdout := *outPath == "-"
	if !toStdout {
		f, err := os.Create(*outPath)
		if err != nil {
			fatal("create output: %v", err)
		}
		defer f.Close()
		out = f
	}

	switch *format {
	case "text":
		renderText(out, rep)
	case "html":
		if err := tmpl.Execute(out, rep); err != nil {
			fatal("render: %v", err)
		}
	default:
		fatal("unknown format %q (use html|text)", *format)
	}

	if !toStdout {
		fmt.Printf("report written to %s (%d cases: %d pass, %d fail, %d skip)\n",
			*outPath, rep.Total, rep.Passed, rep.Failed, rep.Skipped)
	}
	if rep.Failed > 0 {
		os.Exit(1)
	}
}

func parseOutput(tc *testCase, raw string) {
	line := strings.TrimRight(raw, "\n")
	trimmed := strings.TrimSpace(line)
	// Strip the leading "    case_test.go:NN: " prefix that go test adds to t.Log.
	if idx := strings.Index(trimmed, ": "); idx > 0 && strings.Contains(trimmed[:idx], ".go:") {
		trimmed = strings.TrimSpace(trimmed[idx+2:])
	}

	switch {
	case strings.HasPrefix(trimmed, mDesc):
		tc.Description = strings.TrimPrefix(trimmed, mDesc)
	case strings.HasPrefix(trimmed, mStep):
		tc.Steps = append(tc.Steps, strings.TrimPrefix(trimmed, mStep))
	case strings.HasPrefix(trimmed, mInfo):
		tc.Infos = append(tc.Infos, strings.TrimPrefix(trimmed, mInfo))
	case strings.HasPrefix(trimmed, mMetric):
		body := strings.TrimPrefix(trimmed, mMetric)
		tc.Metrics = append(tc.Metrics, parseMetric(body))
	case strings.HasPrefix(trimmed, mCheck):
		body := strings.TrimPrefix(trimmed, mCheck)
		ok := strings.HasPrefix(body, "PASS")
		label := body
		if i := strings.Index(body, " | "); i >= 0 {
			label = body[i+3:]
		}
		tc.Checks = append(tc.Checks, check{OK: ok, Label: label})
	default:
		// Keep meaningful raw lines (skip the === RUN / --- PASS framing).
		if trimmed != "" &&
			!strings.HasPrefix(trimmed, "=== RUN") &&
			!strings.HasPrefix(trimmed, "=== PAUSE") &&
			!strings.HasPrefix(trimmed, "=== CONT") &&
			!strings.HasPrefix(trimmed, "--- PASS") &&
			!strings.HasPrefix(trimmed, "--- FAIL") &&
			!strings.HasPrefix(trimmed, "--- SKIP") &&
			!strings.HasPrefix(trimmed, "PASS") &&
			!strings.HasPrefix(trimmed, "FAIL") &&
			!strings.HasPrefix(trimmed, "ok  ") {
			tc.RawOutput = append(tc.RawOutput, trimmed)
		}
	}
}

// parseMetric parses "name=value|unit".
func parseMetric(s string) metric {
	m := metric{}
	rest := s
	if i := strings.LastIndex(s, "|"); i >= 0 {
		m.Unit = strings.TrimSpace(s[i+1:])
		rest = s[:i]
	}
	if i := strings.Index(rest, "="); i >= 0 {
		m.Name = strings.TrimSpace(rest[:i])
		m.Value = strings.TrimSpace(rest[i+1:])
	} else {
		m.Name = strings.TrimSpace(rest)
	}
	return m
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "report-gen: "+format+"\n", args...)
	os.Exit(2)
}

var tmpl = template.Must(template.New("report").Funcs(template.FuncMap{
	"pct": func(part, total int) int {
		if total == 0 {
			return 0
		}
		return part * 100 / total
	},
	"dur": func(sec float64) string {
		return (time.Duration(sec * float64(time.Second))).Round(time.Millisecond).String()
	},
	"caseLabel": func(name string) string {
		n := strings.TrimPrefix(name, "TestCase")
		n = strings.ReplaceAll(n, "_", " ")
		return n
	},
}).Parse(htmlTemplate))
