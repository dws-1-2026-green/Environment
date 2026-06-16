package suite

import (
	"sort"
	"time"
)

// latencyStats holds summary statistics over a set of durations.
type latencyStats struct {
	Count int
	Min   time.Duration
	Max   time.Duration
	Avg   time.Duration
	P50   time.Duration
	P90   time.Duration
	P95   time.Duration
	P99   time.Duration
}

// computeStats returns percentile statistics over the given durations.
func computeStats(d []time.Duration) latencyStats {
	s := latencyStats{Count: len(d)}
	if len(d) == 0 {
		return s
	}
	sorted := make([]time.Duration, len(d))
	copy(sorted, d)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	s.Min = sorted[0]
	s.Max = sorted[len(sorted)-1]
	var sum time.Duration
	for _, v := range sorted {
		sum += v
	}
	s.Avg = sum / time.Duration(len(sorted))
	s.P50 = percentile(sorted, 0.50)
	s.P90 = percentile(sorted, 0.90)
	s.P95 = percentile(sorted, 0.95)
	s.P99 = percentile(sorted, 0.99)
	return s
}

// percentile returns the p-th percentile (0..1) of a pre-sorted slice.
func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(p * float64(len(sorted)-1))
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}
