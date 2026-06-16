package suite

import (
	"testing"
	"time"
)

// Case 4 — Множество подписчиков на событие.
//
// N subscriptions on the same (source, event_type). A single event must
// fan out to all N subscribers. The fan-out factor N is configurable via
// E2E_FANOUT (default 5).
func TestCase4_MultipleSubscribers(t *testing.T) {
	r := NewReporter(t, "Fan-out: несколько подписчиков на одно событие — каждое событие должно прийти всем подписчикам.")

	n := envInt("E2E_FANOUT", 5)
	if n < 2 {
		n = 2
	}
	source := newSource("case4")
	eventType := "order.created"
	cleanupSource(t, source)

	eps := make([]*Endpoint, n)
	for i := 0; i < n; i++ {
		eps[i] = listener.Register(AlwaysOK())
		id, err := client.CreateSubscription(source, eventType, eps[i].URL(listener.Base()))
		if err != nil {
			r.Fatalf("create subscription %d: %v", i, err)
		}
		r.Step("Подписка #%d (%s) → %s", i+1, id, eps[i].URL(listener.Base()))
	}
	r.Info("Создано %d подписок на (%s, %s)", n, source, eventType)

	sentAt := time.Now()
	eventID, status, err := client.SendEvent(source, eventType, map[string]any{"order_id": "fanout-1"})
	if err != nil {
		r.Fatalf("send event: %v", err)
	}
	r.Check(cfg.IsAccepted(status), "событие принято event-receiver (HTTP %d)", status)
	r.Step("Отправлено одно событие %s, ожидаем %d доставок", eventID, n)

	// Each endpoint must receive the single event.
	delivered := 0
	var maxLatency time.Duration
	for i, ep := range eps {
		c, ok := ep.WaitForCount(1, cfg.DeliveryTimeout)
		if r.Check(ok, "подписчик #%d получил событие (%d/1)", i+1, c) {
			delivered++
			if got := ep.HasCorrelation(eventID); !got {
				r.Check(false, "подписчик #%d получил корректный payload", i+1)
			}
			if lat := ep.Deliveries()[0].At.Sub(sentAt); lat > maxLatency {
				maxLatency = lat
			}
		}
	}

	r.Check(delivered == n, "событие доставлено всем %d подписчикам (фактически %d)", n, delivered)
	r.Metric("subscribers", n, "")
	r.Metric("delivered", delivered, "")
	r.Metric("max_fanout_latency", maxLatency.Milliseconds(), "ms")
	r.Info("Fan-out 1 событие → %d доставок, max latency %s", delivered, maxLatency.Round(time.Millisecond))
}
