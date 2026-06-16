package suite

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Case 5 — Изменение подписки параллельно с отправкой событий.
//
// Главный инвариант: at-least-once. Сообщения могут ДУБЛИРОВАТЬСЯ, но не
// ТЕРЯТЬСЯ, даже когда маршрут меняется прямо во время потока событий.
//
// Топология (две подписки на один (source, event_type), fan-out ×2):
//   - C (stable)   — создаётся и не меняется → обязана получить ВСЕ события
//                    (надёжный якорь «ничего не потеряно»).
//   - M (mutating) — её destination_url переключается A→B одним PUT в середине
//                    непрерывного потока. Объединение A∪B обязано покрыть ВСЕ
//                    события; дополнительно мы наблюдаем, как и когда трафик
//                    реально переехал с A на B.
//
// Настройки: E2E_CHANGE_EVENTS, E2E_CHANGE_RPS, E2E_CHANGE_AT (доля потока, на
// которой делается переключение, 0..1), E2E_CHANGE_SETTLE (доп. ожидание после
// потока, чтобы зафиксировать применение смены).
func TestCase5_ConcurrentSubscriptionChange(t *testing.T) {
	events := envInt("E2E_CHANGE_EVENTS", 225)
	rps := envInt("E2E_CHANGE_RPS", 3)
	switchAt := envFloat("E2E_CHANGE_AT", 0.12)
	settle := envDuration("E2E_CHANGE_SETTLE", 5*time.Second)
	drain := envDuration("E2E_DRAIN_TIMEOUT", 60*time.Second)
	// Bug-flag SLO: насколько быстро смена destination должна применяться на пути
	// доставки. На staging worker кэширует подписки ~48s — это превышает SLO и
	// помечается как баг, не ломая основной инвариант at-least-once.
	maxProp := envDuration("E2E_CHANGE_MAX_PROPAGATION", 5*time.Second)

	r := NewReporter(t, fmt.Sprintf(
		"Изменение подписки на лету: непрерывный поток %d событий @ %d rps; на %.0f%% потока "+
			"destination второй подписки переключается A→B одним PUT. Поток длится дольше TTL "+
			"кэша подписок, поэтому переезд A→B наблюдается прямо во время отправки. "+
			"Инвариант — at-least-once: дубликаты допустимы, потерь нет.",
		events, rps, switchAt*100))

	source := newSource("case5")
	eventType := "order.created"
	cleanupSource(t, source)

	stable := listener.Register(AlwaysOK()) // C
	sinkA := listener.Register(AlwaysOK())  // M → A (до смены)
	sinkB := listener.Register(AlwaysOK())  // M → B (после смены)

	stableID, err := client.CreateSubscription(source, eventType, stable.URL(listener.Base()))
	if err != nil {
		r.Fatalf("create stable subscription: %v", err)
	}
	r.Step("Создана stable-подписка %s → C (не меняется)", stableID)

	mutID, err := client.CreateSubscription(source, eventType, sinkA.URL(listener.Base()))
	if err != nil {
		r.Fatalf("create mutating subscription: %v", err)
	}
	r.Step("Создана mutating-подписка %s → A", mutID)

	switchIdx := int(float64(events) * switchAt)
	sentCorrs := make([]string, events)
	var accepted int64
	var switched atomic.Bool
	var switchErr atomic.Value // error
	var switchTime atomic.Int64

	r.Step("Старт непрерывного потока; переключение A→B запланировано на событии #%d", switchIdx)

	interval := time.Second / time.Duration(rps)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var wg sync.WaitGroup

	for i := 0; i < events; i++ {
		<-ticker.C

		// В середине потока — единственное переключение маршрута, ПАРАЛЛЕЛЬНО
		// продолжающейся отправке (PUT в отдельной горутине, поток не ждёт).
		if i == switchIdx {
			wg.Add(1)
			go func() {
				defer wg.Done()
				t0 := time.Now()
				if e := client.UpdateSubscriptionDestination(mutID, sinkB.URL(listener.Base())); e != nil {
					switchErr.Store(e)
				} else {
					switched.Store(true)
					switchTime.Store(t0.UnixMilli())
				}
			}()
		}

		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			eventID, status, err := client.SendEvent(source, eventType, map[string]any{"seq": idx})
			if err != nil {
				return
			}
			if cfg.IsAccepted(status) {
				sentCorrs[idx] = eventID
				atomic.AddInt64(&accepted, 1)
			}
		}(i)
	}
	wg.Wait()

	if e, ok := switchErr.Load().(error); ok && e != nil {
		r.Fatalf("subscription switch PUT failed: %v", e)
	}
	r.Check(switched.Load(), "переключение подписки A→B выполнено во время потока")
	r.Step("Поток завершён: принято %d. Ожидание применения смены (settle %s)…", accepted, settle)
	time.Sleep(settle)

	expected := make(map[string]bool)
	for _, c := range sentCorrs {
		if c != "" {
			expected[c] = true
		}
	}

	// Drain: ждём, пока C и (A∪B) покроют все события.
	r.Step("Ожидание доставок (drain ≤ %s)…", drain)
	deadline := time.Now().Add(drain)
	for {
		if covers(corrSet(stable), expected) && covers(corrSet(sinkA, sinkB), expected) {
			break
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}

	stableSet := corrSet(stable)
	unionSet := corrSet(sinkA, sinkB)
	stableMissing := missing(expected, stableSet)
	unionMissing := missing(expected, unionSet)

	stableTotal := stable.Count()
	aCount := sinkA.Count()
	bCount := sinkB.Count()
	stableDup := stableTotal - len(stableSet)
	mutDup := (aCount + bCount) - len(unionSet)

	// --- Главные инварианты ---------------------------------------------------
	r.Check(len(stableMissing) == 0,
		"stable-подписка получила ВСЕ события без потерь (потеряно %d/%d)", len(stableMissing), len(expected))
	r.Check(len(unionMissing) == 0,
		"на меняющемся маршруте A∪B покрыли ВСЕ события без потерь (потеряно %d/%d)", len(unionMissing), len(expected))

	// --- Смена реально применилась --------------------------------------------
	r.Check(bCount > 0, "после смены трафик переехал на B (B получил %d)", bCount)
	if aCount > 0 && bCount > 0 {
		r.Info("Маршрут переключился наблюдаемо: A получил %d (до смены), B получил %d (после смены)", aCount, bCount)
	}

	// Задержка применения смены: PUT → первая доставка на B.
	var propagation time.Duration
	if firstB := firstDeliveryAt(sinkB); !firstB.IsZero() && switchTime.Load() > 0 {
		propagation = firstB.Sub(time.UnixMilli(switchTime.Load()))
		if propagation > 0 {
			r.Metric("change_propagation", propagation.Milliseconds(), "ms")
			r.Info("Задержка применения смены (PUT → первая доставка на B): %s", propagation.Round(time.Millisecond))
		}
	}

	// --- BUG FLAG: скорость применения смены ----------------------------------
	// Отдельная проверка SLO. На staging worker кэширует подписки (~48s), что
	// много выше разумного порога — помечаем как баг. Основной инвариант
	// at-least-once при этом остаётся зелёным.
	if bCount > 0 {
		r.Check(propagation <= maxProp,
			"⚠ BUG worker-cache: смена destination применяется за %s (SLO ≤ %s)",
			propagation.Round(time.Millisecond), maxProp)
	}

	r.Metric("events_accepted", accepted, "")
	r.Metric("switch_at_event", switchIdx, "")
	r.Metric("stable_delivered", stableTotal, "")
	r.Metric("stable_duplicates", stableDup, "")
	r.Metric("before_change_A", aCount, "")
	r.Metric("after_change_B", bCount, "")
	r.Metric("mutating_duplicates", mutDup, "")
	r.Metric("lost", len(stableMissing)+len(unionMissing), "")

	r.Info("Дубликаты (допустимы): stable=%d, mutating=%d. Потерь: %d",
		stableDup, mutDup, len(stableMissing)+len(unionMissing))
}

// firstDeliveryAt returns the timestamp of the earliest delivery to ep (zero if none).
func firstDeliveryAt(ep *Endpoint) time.Time {
	var first time.Time
	for _, d := range ep.Deliveries() {
		if first.IsZero() || d.At.Before(first) {
			first = d.At
		}
	}
	return first
}

// corrSet returns the set of unique correlation ids delivered to the given endpoints.
func corrSet(eps ...*Endpoint) map[string]bool {
	set := make(map[string]bool)
	for _, ep := range eps {
		for _, d := range ep.Deliveries() {
			if d.Corr != "" {
				set[d.Corr] = true
			}
		}
	}
	return set
}

func covers(set, want map[string]bool) bool {
	for k := range want {
		if !set[k] {
			return false
		}
	}
	return true
}

func missing(want, set map[string]bool) []string {
	var out []string
	for k := range want {
		if !set[k] {
			out = append(out, k)
		}
	}
	return out
}
