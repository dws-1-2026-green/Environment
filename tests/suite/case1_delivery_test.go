package suite

import (
	"testing"
	"time"
)

// Case 1 — Успешная доставка (базовый случай).
//
// One subscription, one event. Verifies the full pipeline
// (event-receiver → Kafka → subscriptions-worker → delivery-service → webhook)
// delivers the event payload to the subscriber exactly once, and measures the
// end-to-end delivery latency.
func TestCase1_SuccessfulDelivery(t *testing.T) {
	r := NewReporter(t, "Базовый случай: одно событие, один подписчик, успешная доставка end-to-end.")

	source := newSource("case1")
	eventType := "order.created"
	cleanupSource(t, source)

	// 1. Register a healthy subscriber.
	ep := listener.Register(AlwaysOK())
	destURL := ep.URL(listener.Base())
	subID, err := client.CreateSubscription(source, eventType, destURL)
	if err != nil {
		r.Fatalf("create subscription: %v", err)
	}
	r.Step("Создана подписка %s: source=%s type=%s → %s", subID, source, eventType, destURL)

	// 2. Send one event.
	data := map[string]any{"order_id": "12345", "amount": 1990, "currency": "RUB"}
	sentAt := time.Now()
	eventID, status, err := client.SendEvent(source, eventType, data)
	if err != nil {
		r.Fatalf("send event: %v", err)
	}
	r.Check(cfg.IsAccepted(status), "событие принято event-receiver (HTTP %d)", status)
	r.Step("Отправлено событие %s", eventID)

	// 3. Wait for exactly one delivery.
	r.Step("Ожидание доставки вебхука (timeout %s)…", cfg.DeliveryTimeout)
	count, ok := ep.WaitForCount(1, cfg.DeliveryTimeout)
	if !r.Check(ok, "вебхук доставлен подписчику (получено %d/1)", count) {
		return
	}
	latency := time.Since(sentAt)

	// 4. Verify payload correctness — delivery carries only event.data.
	deliveries := ep.Deliveries()
	d := deliveries[0]
	r.Check(d.Corr == eventID, "доставленный payload соответствует отправленному событию (corr=%s)", d.Corr)
	r.Check(d.Body["order_id"] == "12345", "поле order_id доставлено корректно (%v)", d.Body["order_id"])

	// 5. Confirm no duplicate deliveries within a short window.
	dupCount, _ := ep.WaitForCount(2, 5*time.Second)
	r.Check(dupCount == 1, "нет дублирующих доставок (всего получено %d)", dupCount)

	r.Metric("delivery_latency", latency.Milliseconds(), "ms")
	r.Metric("deliveries_received", ep.Count(), "")
	r.Info("Событие доставлено за %s", latency.Round(time.Millisecond))
}
