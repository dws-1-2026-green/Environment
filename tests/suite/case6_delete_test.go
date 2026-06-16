package suite

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Case 6 — Удаление одной из подписок во время потока событий (DELETE).
//
// Два подписчика на один (source, event_type): A (выживает) и B (удаляется).
// В середине непрерывного потока B удаляется через DELETE /{id}. Проверяем, что
// в «хвосте» потока — события, отправленные заведомо после распространения
// удаления — приходят ТОЛЬКО на A, а на удалённую подписку B перестают.
//
// Поток длится дольше TTL кэша подписок worker'а (~48s на staging), иначе
// удаление не успеет примениться и хвост ещё будет попадать на B.
//
// Настройки: E2E_DELETE_EVENTS, E2E_DELETE_RPS, E2E_DELETE_AT (доля потока для
// удаления), E2E_DELETE_TAIL_FRACTION (доля хвоста для проверки «B молчит»).
func TestCase6_DeleteSubscription(t *testing.T) {
	events := envInt("E2E_DELETE_EVENTS", 225)
	rps := envInt("E2E_DELETE_RPS", 3)
	deleteAt := envFloat("E2E_DELETE_AT", 0.12)
	tailFrac := envFloat("E2E_DELETE_TAIL_FRACTION", 0.2)
	settle := envDuration("E2E_DELETE_SETTLE", 5*time.Second)
	drain := envDuration("E2E_DRAIN_TIMEOUT", 60*time.Second)

	r := NewReporter(t, fmt.Sprintf(
		"Удаление подписки на лету: два подписчика на одно событие, непрерывный поток %d @ %d rps; "+
			"на %.0f%% потока одна подписка удаляется (DELETE). В хвосте потока события должны "+
			"приходить ТОЛЬКО оставшемуся; удалённый перестаёт их получать. Выживший — без потерь.",
		events, rps, deleteAt*100))

	source := newSource("case6")
	eventType := "order.created"
	cleanupSource(t, source)

	survivor := listener.Register(AlwaysOK()) // A — остаётся
	deleted := listener.Register(AlwaysOK())  // B — будет удалён

	keepID, err := client.CreateSubscription(source, eventType, survivor.URL(listener.Base()))
	if err != nil {
		r.Fatalf("create survivor subscription: %v", err)
	}
	r.Step("Создана подписка A %s → survivor (остаётся)", keepID)

	delID, err := client.CreateSubscription(source, eventType, deleted.URL(listener.Base()))
	if err != nil {
		r.Fatalf("create to-be-deleted subscription: %v", err)
	}
	r.Step("Создана подписка B %s → будет удалена", delID)

	deleteIdx := int(float64(events) * deleteAt)
	sentCorrs := make([]string, events)
	sentTimes := make([]time.Time, events)
	var accepted int64
	var delDone atomic.Bool
	var delErr atomic.Value
	var delTime atomic.Int64

	r.Step("Старт непрерывного потока; удаление B запланировано на событии #%d", deleteIdx)

	interval := time.Second / time.Duration(rps)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var wg sync.WaitGroup

	for i := 0; i < events; i++ {
		<-ticker.C

		if i == deleteIdx {
			wg.Add(1)
			go func() {
				defer wg.Done()
				t0 := time.Now()
				if e := client.DeleteSubscription(delID); e != nil {
					delErr.Store(e)
				} else {
					delDone.Store(true)
					delTime.Store(t0.UnixMilli())
				}
			}()
		}

		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			t0 := time.Now()
			eventID, status, err := client.SendEvent(source, eventType, map[string]any{"seq": idx})
			if err != nil {
				return
			}
			if cfg.IsAccepted(status) {
				sentCorrs[idx] = eventID
				sentTimes[idx] = t0
				atomic.AddInt64(&accepted, 1)
			}
		}(i)
	}
	wg.Wait()

	if e, ok := delErr.Load().(error); ok && e != nil {
		r.Fatalf("DELETE subscription failed: %v", e)
	}
	r.Check(delDone.Load(), "удаление подписки B выполнено во время потока (DELETE → 204)")
	r.Step("Поток завершён: принято %d. Ожидание применения удаления (settle %s)…", accepted, settle)
	time.Sleep(settle)

	expected := make(map[string]bool)
	for _, c := range sentCorrs {
		if c != "" {
			expected[c] = true
		}
	}

	// Drain: ждём, пока выживший A покроет все события.
	r.Step("Ожидание доставок на выжившего A (drain ≤ %s)…", drain)
	deadline := time.Now().Add(drain)
	for {
		if covers(corrSet(survivor), expected) || time.Now().After(deadline) {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}

	aSet := corrSet(survivor)
	bSet := corrSet(deleted)
	aMissing := missing(expected, aSet)
	aCount := survivor.Count()
	bCount := deleted.Count()

	// --- Инвариант: выживший получил всё без потерь ---------------------------
	r.Check(len(aMissing) == 0,
		"оставшийся подписчик A получил ВСЕ события без потерь (потеряно %d/%d)", len(aMissing), len(expected))

	// --- Удалённый был активен до удаления -------------------------------------
	r.Check(bCount > 0, "до удаления подписка B действительно получала события (получено %d)", bCount)

	// --- Хвост потока: события идут только на A, на B — больше нет -------------
	tailStart := int(float64(events) * (1 - tailFrac))
	tailTotal, tailLeakToB, tailMissA := 0, 0, 0
	for idx := tailStart; idx < events; idx++ {
		c := sentCorrs[idx]
		if c == "" {
			continue
		}
		tailTotal++
		if bSet[c] {
			tailLeakToB++
		}
		if !aSet[c] {
			tailMissA++
		}
	}
	if tailTotal == 0 {
		r.Fatalf("хвост пуст — увеличьте E2E_DELETE_EVENTS/длительность потока")
	}
	r.Check(tailLeakToB == 0,
		"после удаления B не получил НИ ОДНОГО из %d событий хвоста (утечек на удалённую подписку: %d)",
		tailTotal, tailLeakToB)
	r.Check(tailMissA == 0,
		"все %d событий хвоста дошли до выжившего A (недоставлено: %d)", tailTotal, tailMissA)

	// --- Когда именно B перестал получать (задержка применения удаления) ------
	lastBIdx := -1
	for idx := 0; idx < events; idx++ {
		if c := sentCorrs[idx]; c != "" && bSet[c] {
			lastBIdx = idx
		}
	}
	if lastBIdx >= 0 && delTime.Load() > 0 && !sentTimes[lastBIdx].IsZero() {
		prop := sentTimes[lastBIdx].Sub(time.UnixMilli(delTime.Load()))
		if prop > 0 {
			r.Metric("delete_propagation", prop.Milliseconds(), "ms")
			r.Info("Задержка применения удаления (DELETE → последнее событие, дошедшее до B): %s", prop.Round(time.Millisecond))
		}
	}

	r.Metric("events_accepted", accepted, "")
	r.Metric("delete_at_event", deleteIdx, "")
	r.Metric("survivor_delivered", aCount, "")
	r.Metric("deleted_before_stop", bCount, "")
	r.Metric("tail_events", tailTotal, "")
	r.Metric("tail_leak_to_deleted", tailLeakToB, "")

	r.Info("После удаления хвост (%d событий) ушёл только на A; на удалённую B — 0 утечек", tailTotal)
}
