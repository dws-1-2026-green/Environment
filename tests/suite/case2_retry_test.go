package suite

import (
	"net/http"
	"testing"
	"time"
)

// Case 2 — Ретраи (сервер отвечает 500).
//
// A subscriber that returns 5xx for the first N attempts and then 200.
// Verifies delivery-service retries with backoff and eventually succeeds.
// The number of failing attempts before success is configurable via
// E2E_MIN_RETRY_ATTEMPTS (the test fails the first MinRetryAttempts-1 calls,
// then succeeds, so it proves at least that many attempts happened).
func TestCase2_RetryOn5xx(t *testing.T) {
	r := NewReporter(t, "Ретраи: подписчик отвечает 500 несколько раз, затем 200 — delivery-service должен повторять доставку с backoff и в итоге доставить.")

	source := newSource("case2")
	eventType := "order.created"
	cleanupSource(t, source)

	// Fail the first (MinRetryAttempts-1) attempts, then succeed. This guarantees
	// the test needs at least MinRetryAttempts deliveries to pass.
	failFirst := cfg.MinRetryAttempts - 1
	if failFirst < 1 {
		failFirst = 1
	}
	ep := listener.Register(FailNThenOK(failFirst, http.StatusInternalServerError))
	destURL := ep.URL(listener.Base())

	subID, err := client.CreateSubscription(source, eventType, destURL)
	if err != nil {
		r.Fatalf("create subscription: %v", err)
	}
	r.Step("Создана подписка %s → %s (отвечает 500 первые %d раз, затем 200)", subID, destURL, failFirst)

	eventID, status, err := client.SendEvent(source, eventType, nil)
	if err != nil {
		r.Fatalf("send event: %v", err)
	}
	r.Check(cfg.IsAccepted(status), "событие принято event-receiver (HTTP %d)", status)
	r.Step("Отправлено событие %s", eventID)

	// Wait for the eventual successful delivery (failFirst failures + 1 success).
	want := failFirst + 1
	r.Step("Ожидание ретраев и финальной успешной доставки (нужно %d попыток, timeout %s)…", want, cfg.RetryWaitTimeout)
	count, ok := ep.WaitForCount(want, cfg.RetryWaitTimeout)
	if !r.Check(ok, "получено достаточно попыток доставки (%d/%d)", count, want) {
		r.dumpAttempts(ep)
		return
	}

	deliveries := ep.Deliveries()
	r.Check(count >= cfg.MinRetryAttempts, "выполнено минимум %d попыток (фактически %d)", cfg.MinRetryAttempts, count)

	// Verify the last attempt was the one we answered 200 to.
	last := deliveries[len(deliveries)-1]
	r.Check(last.Status == http.StatusOK, "последняя попытка завершилась успехом (HTTP %d)", last.Status)

	// Measure backoff between the first two attempts.
	if len(deliveries) >= 2 {
		backoff := deliveries[1].At.Sub(deliveries[0].At)
		r.Metric("first_retry_backoff", backoff.Milliseconds(), "ms")
		r.Info("Интервал до первого ретрая: %s", backoff.Round(time.Millisecond))
	}
	totalSpan := deliveries[len(deliveries)-1].At.Sub(deliveries[0].At)
	r.Metric("attempts_total", count, "")
	r.Metric("retry_span", totalSpan.Milliseconds(), "ms")
	r.dumpAttempts(ep)
}

// dumpAttempts logs each attempt with its returned status for the report.
func (r *Reporter) dumpAttempts(ep *Endpoint) {
	for i, d := range ep.Deliveries() {
		r.Info("попытка #%d @ %s → ответили HTTP %d", i+1, d.At.Format("15:04:05.000"), d.Status)
	}
}
