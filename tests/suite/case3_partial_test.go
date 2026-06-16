package suite

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Case 3 — Принятие части событий (часть подписчиков падает).
//
// Several subscribers on the same (source, event_type). Some always succeed,
// one always returns 4xx (fast-fail, no retry), one always returns 5xx
// (retried but never succeeds). Verifies that:
//   - healthy subscribers receive every event,
//   - a failing subscriber does NOT block or delay the healthy ones,
//   - a 4xx subscriber is not retried, a 5xx subscriber is retried.
func TestCase3_PartialAcceptance(t *testing.T) {
	r := NewReporter(t, "Деградация части подписчиков: здоровые подписчики получают все события, падающие (4xx/5xx) не блокируют пайплайн.")

	source := newSource("case3")
	eventType := "order.created"
	cleanupSource(t, source)

	const numEvents = 5

	// Two healthy, one 4xx fast-fail, one 5xx always-failing.
	good1 := listener.Register(AlwaysOK())
	good2 := listener.Register(AlwaysOK())
	bad4xx := listener.Register(AlwaysStatus(http.StatusBadRequest))
	bad5xx := listener.Register(AlwaysStatus(http.StatusInternalServerError))

	subs := []struct {
		name string
		ep   *Endpoint
	}{
		{"healthy-1", good1},
		{"healthy-2", good2},
		{"failing-4xx", bad4xx},
		{"failing-5xx", bad5xx},
	}
	for _, s := range subs {
		id, err := client.CreateSubscription(source, eventType, s.ep.URL(listener.Base()))
		if err != nil {
			r.Fatalf("create subscription %s: %v", s.name, err)
		}
		r.Step("Подписка %s (%s) → %s", s.name, id, s.ep.URL(listener.Base()))
	}

	// Send a batch of events.
	sentCorrs := make([]string, 0, numEvents)
	for i := 0; i < numEvents; i++ {
		eventID, status, err := client.SendEvent(source, eventType, map[string]any{"order_id": uuid.NewString()[:8]})
		if err != nil {
			r.Fatalf("send event %d: %v", i, err)
		}
		if !cfg.IsAccepted(status) {
			r.Check(false, "событие %d принято (HTTP %d)", i, status)
		}
		sentCorrs = append(sentCorrs, eventID)
	}
	r.Step("Отправлено %d событий", numEvents)

	// Healthy subscribers must each receive all numEvents — even though siblings fail.
	r.Step("Ожидание доставки на здоровые подписчики (timeout %s)…", cfg.DeliveryTimeout)
	c1, ok1 := good1.WaitForCount(numEvents, cfg.DeliveryTimeout)
	c2, ok2 := good2.WaitForCount(numEvents, cfg.DeliveryTimeout)
	r.Check(ok1, "healthy-1 получил все события (%d/%d)", c1, numEvents)
	r.Check(ok2, "healthy-2 получил все события (%d/%d)", c2, numEvents)

	// Verify each sent event actually reached the healthy subscribers.
	missing := 0
	for _, corr := range sentCorrs {
		if !good1.HasCorrelation(corr) {
			missing++
		}
	}
	r.Check(missing == 0, "все %d событий долетели до healthy-1 (потеряно %d)", numEvents, missing)

	// 4xx subscriber: each event attempted exactly once (no retry).
	// Give the scheduler a moment, then assert it did not exceed numEvents.
	time.Sleep(cfg.NoRetryWindow)
	c4 := bad4xx.Count()
	r.Check(c4 == numEvents, "4xx-подписчик не ретраился: %d попыток на %d событий", c4, numEvents)

	// 5xx subscriber: retried, so attempts should exceed numEvents.
	c5 := bad5xx.Count()
	r.Check(c5 > numEvents, "5xx-подписчик ретраился: %d попыток > %d событий", c5, numEvents)

	r.Metric("healthy_1_delivered", c1, "")
	r.Metric("healthy_2_delivered", c2, "")
	r.Metric("bad_4xx_attempts", c4, "")
	r.Metric("bad_5xx_attempts", c5, "")
	r.Info("Падающие подписчики не помешали доставке на здоровые: %d+%d успешных доставок", c1, c2)
}
