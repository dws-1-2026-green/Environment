package main

import (
	"fmt"
	"io"
	"strings"
	"time"
)

// renderText writes a plain-text report — readable directly in a terminal or in
// `kubectl logs` for a Job, no file or web UI needed.
func renderText(w io.Writer, rep *report) {
	const bar = "════════════════════════════════════════════════════════════"
	const thin = "────────────────────────────────────────────────────────────"

	fmt.Fprintln(w, bar)
	fmt.Fprintf(w, " %s\n", rep.Title)
	if rep.Target != "" {
		fmt.Fprintf(w, " Цель: %s\n", rep.Target)
	}
	fmt.Fprintf(w, " Сгенерировано: %s\n", rep.Generated)
	fmt.Fprintf(w, " Итог: %d PASS · %d FAIL · %d SKIP   (всего %d, время %s)\n",
		rep.Passed, rep.Failed, rep.Skipped, rep.Total, durText(rep.Duration))
	fmt.Fprintln(w, bar)

	for _, tc := range rep.Cases {
		fmt.Fprintf(w, "\n[%s] %-40s %8s\n",
			strings.ToUpper(tc.Status), caseLabelText(tc.Name), durText(tc.Elapsed))

		if tc.Description != "" {
			for _, line := range wrap(tc.Description, 72) {
				fmt.Fprintf(w, "   %s\n", line)
			}
		}

		if len(tc.Checks) > 0 {
			fmt.Fprintln(w, "   проверки:")
			for _, c := range tc.Checks {
				mark := "✓"
				if !c.OK {
					mark = "✗"
				}
				fmt.Fprintf(w, "     %s %s\n", mark, c.Label)
			}
		}

		if len(tc.Metrics) > 0 {
			fmt.Fprint(w, "   метрики:")
			for _, m := range tc.Metrics {
				unit := m.Unit
				fmt.Fprintf(w, "  %s=%s%s", m.Name, m.Value, unit)
			}
			fmt.Fprintln(w)
		}

		if len(tc.Infos) > 0 {
			fmt.Fprintln(w, "   детали:")
			for _, info := range tc.Infos {
				fmt.Fprintf(w, "     • %s\n", info)
			}
		}
	}
	fmt.Fprintf(w, "\n%s\n", thin)
	if rep.Failed > 0 {
		fmt.Fprintf(w, " РЕЗУЛЬТАТ: FAIL (%d из %d кейсов упали)\n", rep.Failed, rep.Total)
	} else {
		fmt.Fprintf(w, " РЕЗУЛЬТАТ: OK (%d кейсов прошли)\n", rep.Passed)
	}
	fmt.Fprintln(w, thin)
}

func caseLabelText(name string) string {
	n := strings.TrimPrefix(name, "Test")
	return strings.ReplaceAll(n, "_", " ")
}

func durText(sec float64) string {
	return time.Duration(sec * float64(time.Second)).Round(time.Millisecond).String()
}

// wrap breaks s into lines no longer than width, splitting on spaces.
func wrap(s string, width int) []string {
	words := strings.Fields(s)
	if len(words) == 0 {
		return nil
	}
	var lines []string
	cur := words[0]
	for _, wd := range words[1:] {
		if len(cur)+1+len(wd) > width {
			lines = append(lines, cur)
			cur = wd
		} else {
			cur += " " + wd
		}
	}
	return append(lines, cur)
}
