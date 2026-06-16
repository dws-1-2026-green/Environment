package suite

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Case 5/6 — Closed-loop нагрузочный и стресс-тест.
//
// Unlike a pure ingestion blast, these tests run their own webhook SINK
// (the shared Listener) that absorbs 100% of deliveries and timestamps them.
// That guarantees nothing is left "hanging" in delivery-service, and lets us
// measure the full round-trip: ingestion latency AND delivery latency, plus a
// reconciliation of sent vs delivered (pending = events that never arrived).
//
// They are gated behind env flags so a plain functional run does not trigger
// load. Enable with E2E_RUN_LOAD=true / E2E_RUN_STRESS=true (the docker
// entrypoint sets these for the `load` / `stress` suites).

// loadResult accumulates the reconciliation of one run.
type loadResult struct {
	rps            int
	events         int
	fanout         int
	accepted       int64
	rejected       int64
	sendErrors     int64
	ingestion      []time.Duration
	expectedDeliv  int64
	deliveredCount int
	delivery       []time.Duration
	sendWall       time.Duration
	drainWall      time.Duration
}

func TestLoadClosedLoop(t *testing.T) {
	if !envBool("E2E_RUN_LOAD", false) {
		t.Skip("закрытый load-тест выключен (установите E2E_RUN_LOAD=true)")
	}
	rps := envInt("E2E_LOAD_RPS", 20)
	events := envInt("E2E_LOAD_EVENTS", 100)
	fanout := envInt("E2E_LOAD_FANOUT", 1)
	drain := envDuration("E2E_DRAIN_TIMEOUT", 60*time.Second)

	r := NewReporter(t, fmt.Sprintf(
		"Closed-loop нагрузка: %d событий @ %d rps, fan-out ×%d. Sink принимает все доставки, "+
			"замеряем ingestion- и delivery-latency, сверяем отправлено/доставлено/зависло.",
		events, rps, fanout))

	res := runClosedLoop(t, r, rps, events, fanout, drain)
	reportClosedLoop(r, "load", res)
}

func TestStressClosedLoop(t *testing.T) {
	if !envBool("E2E_RUN_STRESS", false) {
		t.Skip("закрытый stress-тест выключен (установите E2E_RUN_STRESS=true)")
	}
	startRPS := envInt("E2E_STRESS_START_RPS", 20)
	peakRPS := envInt("E2E_STRESS_PEAK_RPS", 100)
	stepEvents := envInt("E2E_STRESS_STEP_EVENTS", 100)
	fanout := envInt("E2E_LOAD_FANOUT", 1)
	drain := envDuration("E2E_DRAIN_TIMEOUT", 90*time.Second)

	r := NewReporter(t, fmt.Sprintf(
		"Closed-loop стресс: ступенчатый рост %d→%d rps по %d событий на ступень, fan-out ×%d. "+
			"На каждой ступени sink принимает все доставки; ищем, где растут latency/pending.",
		startRPS, peakRPS, stepEvents, fanout))

	steps := 4
	for i := 0; i < steps; i++ {
		rps := startRPS + (peakRPS-startRPS)*i/(steps-1)
		r.Step("Ступень %d/%d: %d rps × %d событий", i+1, steps, rps, stepEvents)
		res := runClosedLoop(t, r, rps, stepEvents, fanout, drain)
		r.Metric(fmt.Sprintf("step%d_rps", i+1), rps, "")
		r.Metric(fmt.Sprintf("step%d_deliv_p95", i+1), ms(computeStats(res.delivery).P95), "ms")
		r.Metric(fmt.Sprintf("step%d_pending", i+1), res.expectedDeliv-int64(res.deliveredCount), "")
		if pend := res.expectedDeliv - int64(res.deliveredCount); pend > 0 {
			r.Info("⚠ ступень %d: %d доставок не пришло за drain-таймаут", i+1, pend)
		}
	}
	r.Info("Стресс-прогон завершён — см. метрики по ступеням выше")
}

// runClosedLoop sends events at the target rps, absorbs the resulting
// deliveries on a fresh sink endpoint, and reconciles the two.
func runClosedLoop(t *testing.T, r *Reporter, rps, events, fanout int, drain time.Duration) *loadResult {
	source := newSource("load")
	eventType := "load.event"
	cleanupSource(t, source)

	// One sink endpoint per subscription. Fan-out = number of subscriptions.
	eps := make([]*Endpoint, fanout)
	for i := 0; i < fanout; i++ {
		eps[i] = listener.Register(AlwaysOK())
		if _, err := client.CreateSubscription(source, eventType, eps[i].URL(listener.Base())); err != nil {
			r.Fatalf("create subscription %d: %v", i, err)
		}
	}

	res := &loadResult{rps: rps, events: events, fanout: fanout}
	sendTimes := &sync.Map{} // corr(eventID) -> send start time

	interval := time.Second / time.Duration(rps)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var wg sync.WaitGroup
	var ingMu sync.Mutex

	sendStart := time.Now()
	for i := 0; i < events; i++ {
		<-ticker.C
		wg.Add(1)
		go func() {
			defer wg.Done()
			t0 := time.Now()
			eventID, status, err := client.SendEvent(source, eventType, map[string]any{"seq": i})
			lat := time.Since(t0)
			if err != nil {
				atomic.AddInt64(&res.sendErrors, 1)
				return
			}
			sendTimes.Store(eventID, t0)
			ingMu.Lock()
			res.ingestion = append(res.ingestion, lat)
			ingMu.Unlock()
			if cfg.IsAccepted(status) {
				atomic.AddInt64(&res.accepted, 1)
			} else {
				atomic.AddInt64(&res.rejected, 1)
			}
		}()
	}
	wg.Wait()
	res.sendWall = time.Since(sendStart)
	res.expectedDeliv = res.accepted * int64(fanout)

	// Drain: wait until all expected deliveries arrive (or timeout) so nothing
	// is left hanging unnoticed.
	r.Step("Отправка завершена (%d принято). Ожидание %d доставок на sink (drain ≤ %s)…",
		res.accepted, res.expectedDeliv, drain)
	drainStart := time.Now()
	deadline := drainStart.Add(drain)
	for {
		got := 0
		for _, ep := range eps {
			got += ep.Count()
		}
		if int64(got) >= res.expectedDeliv || time.Now().After(deadline) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	res.drainWall = time.Since(drainStart)

	// Join deliveries with their send time to compute delivery latency.
	for _, ep := range eps {
		for _, d := range ep.Deliveries() {
			res.deliveredCount++
			if v, ok := sendTimes.Load(d.Corr); ok {
				res.delivery = append(res.delivery, d.At.Sub(v.(time.Time)))
			}
		}
	}
	return res
}

func reportClosedLoop(r *Reporter, label string, res *loadResult) {
	ing := computeStats(res.ingestion)
	dlv := computeStats(res.delivery)
	pending := res.expectedDeliv - int64(res.deliveredCount)

	sendTput := float64(res.accepted) / res.sendWall.Seconds()
	delivTput := 0.0
	if res.drainWall.Seconds() > 0 {
		delivTput = float64(res.deliveredCount) / (res.sendWall.Seconds() + res.drainWall.Seconds())
	}

	// Throughput / volume
	r.Metric("target_rps", res.rps, "")
	r.Metric("events_sent", res.events, "")
	r.Metric("accepted", res.accepted, "")
	r.Metric("send_throughput", round1(sendTput), "rps")
	r.Metric("fanout", res.fanout, "")

	// Ingestion latency
	r.Metric("ingest_p50", ms(ing.P50), "ms")
	r.Metric("ingest_p95", ms(ing.P95), "ms")
	r.Metric("ingest_p99", ms(ing.P99), "ms")
	r.Metric("ingest_max", ms(ing.Max), "ms")

	// Delivery latency (the round-trip the user cares about)
	r.Metric("deliver_p50", ms(dlv.P50), "ms")
	r.Metric("deliver_p95", ms(dlv.P95), "ms")
	r.Metric("deliver_p99", ms(dlv.P99), "ms")
	r.Metric("deliver_max", ms(dlv.Max), "ms")
	r.Metric("delivery_throughput", round1(delivTput), "rps")

	// Reconciliation — the "nothing left hanging" check
	r.Metric("expected_deliveries", res.expectedDeliv, "")
	r.Metric("delivered", res.deliveredCount, "")
	r.Metric("pending_hanging", pending, "")

	if res.rejected > 0 {
		r.Metric("rejected", res.rejected, "")
	}
	if res.sendErrors > 0 {
		r.Metric("send_errors", res.sendErrors, "")
	}

	r.Check(pending == 0, "все доставки получены, ничего не зависло (pending=%d)", pending)
	r.Check(res.rejected == 0, "event-receiver не отклонял события (rejected=%d)", res.rejected)

	r.Info("ingestion: p50=%s p95=%s p99=%s | delivery: p50=%s p95=%s p99=%s",
		ing.P50.Round(time.Millisecond), ing.P95.Round(time.Millisecond), ing.P99.Round(time.Millisecond),
		dlv.P50.Round(time.Millisecond), dlv.P95.Round(time.Millisecond), dlv.P99.Round(time.Millisecond))
	r.Info("throughput: send=%.1f rps, delivery=%.1f rps | drain=%s",
		sendTput, delivTput, res.drainWall.Round(time.Millisecond))
}

// ms converts a duration to integer milliseconds.
func ms(d time.Duration) int64 { return d.Milliseconds() }

// round1 rounds a float to one decimal place.
func round1(f float64) float64 { return float64(int(f*10+0.5)) / 10 }
