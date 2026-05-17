package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

// stressMock is a webhook receiver that randomly returns 200 (80%), 400 (10%), 500 (10%).
// Counters are safe for concurrent use.
type stressMock struct {
	server  *httptest.Server
	total   atomic.Int64
	ok200   atomic.Int64
	fail400 atomic.Int64
	fail500 atomic.Int64
}

func newStressMock() *stressMock {
	m := &stressMock{}
	m.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.ReadAll(r.Body)
		m.total.Add(1)
		switch rand.IntN(10) {
		case 0: // 10% → 400 fast-fail
			m.fail400.Add(1)
			w.WriteHeader(http.StatusBadRequest)
		case 1: // 10% → 500 retried by delivery-service
			m.fail500.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
		default: // 80% → success
			m.ok200.Add(1)
			w.WriteHeader(http.StatusOK)
		}
	}))
	return m
}

// dockerURL returns the address reachable from inside Docker containers.
func (m *stressMock) dockerURL() string {
	u, _ := url.Parse(m.server.URL)
	return fmt.Sprintf("http://host.docker.internal:%s", u.Port())
}

// stressProfile binds a unique (source, eventType) to a set of mock indexes.
// Each event published to this source fans out to len(mockIdxs) webhooks.
type stressProfile struct {
	source    string
	eventType string
	mockIdxs  []int // indexes into the mocks slice
}

// ---------------------------------------------------------------------------
// Top-level test entry points — run individually with -run TestStress_10RPS etc.
// ---------------------------------------------------------------------------

func TestStress_10RPS(t *testing.T)  { runStress(t, 10, 150, 4*time.Minute) }
func TestStress_50RPS(t *testing.T)  { runStress(t, 50, 750, 15*time.Minute) }
func TestStress_100RPS(t *testing.T) { runStress(t, 100, 1500, 25*time.Minute) }

// runStress is the shared stress-test body.
//
// Subscription topology (5 mocks total):
//   - fanout : source A → mocks[0,1,2]   (3 webhooks per event)
//   - single : source B → mocks[3]        (1 webhook per event)
//   - dual   : source C → mocks[4,0]      (2 webhooks per event; mocks[0] shared on purpose)
//
// Events are distributed round-robin across the three profiles, so the
// average fan-out is (3+1+2)/3 = 2 webhooks per event.
func runStress(t *testing.T, rps, numEvents int, waitTimeout time.Duration) {
	t.Helper()

	// Skip gracefully when the stack is not running.
	hc := &http.Client{Timeout: 3 * time.Second}
	if resp, err := hc.Get(eventReceiverURL + "/health"); err != nil {
		t.Skipf("EventReceiver not reachable: %v", err)
	} else {
		resp.Body.Close()
	}

	// --- 1. Spin up 5 mock servers ----------------------------------------

	const numMocks = 5
	mocks := make([]*stressMock, numMocks)
	for i := range mocks {
		mocks[i] = newStressMock()
	}
	defer func() {
		for _, m := range mocks {
			m.server.Close()
		}
	}()

	// --- 2. Define subscription profiles ------------------------------------

	profiles := []stressProfile{
		{
			source:    "stress-fanout-" + uuid.NewString()[:8],
			eventType: "stress.order",
			mockIdxs:  []int{0, 1, 2}, // 3 webhooks per event
		},
		{
			source:    "stress-single-" + uuid.NewString()[:8],
			eventType: "stress.order",
			mockIdxs:  []int{3}, // 1 webhook per event
		},
		{
			source:    "stress-dual-" + uuid.NewString()[:8],
			eventType: "stress.order",
			mockIdxs:  []int{4, 0}, // 2 webhooks per event; mocks[0] gets extra load
		},
	}

	// --- 3. Register subscriptions in Cassandra via Subscriptions API -------

	for _, p := range profiles {
		for _, idx := range p.mockIdxs {
			id := createSubscription(t, p.source, p.eventType, mocks[idx].dockerURL())
			t.Logf("sub %-38s  %s → mock[%d]", id, p.source, idx)
		}
	}

	// --- 4. Pre-compute expected first-attempt deliveries -------------------
	// One delivery attempt per subscription per event, regardless of response code.
	// 500 responses will generate additional retry attempts on top of this baseline.

	var expectedFirstAttempts int64
	for i := range numEvents {
		expectedFirstAttempts += int64(len(profiles[i%len(profiles)].mockIdxs))
	}
	t.Logf("plan: %d events @ ~%d rps → %d expected first-attempt deliveries",
		numEvents, rps, expectedFirstAttempts)

	// --- 5. Send events at target RPS ---------------------------------------

	sendStart := time.Now()
	ticker := time.NewTicker(time.Second / time.Duration(rps))
	defer ticker.Stop()

	var sentOK atomic.Int64
	var wg sync.WaitGroup

	for i := range numEvents {
		<-ticker.C // rate-limit: one event per tick
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			p := profiles[idx%len(profiles)]
			b, _ := json.Marshal(map[string]interface{}{
				"id":         uuid.NewString(),
				"type":       p.eventType,
				"created_at": time.Now().UTC().Format(time.RFC3339),
				"data":       map[string]interface{}{"order_id": fmt.Sprintf("order-%d", idx)},
			})
			r, err := http.Post(
				fmt.Sprintf("%s/sources/%s/events", eventReceiverURL, p.source),
				"application/json",
				bytes.NewBuffer(b),
			)
			if err != nil {
				return
			}
			r.Body.Close()
			if r.StatusCode == http.StatusOK || r.StatusCode == http.StatusAccepted {
				sentOK.Add(1)
			}
		}(i)
	}
	wg.Wait()

	sendElapsed := time.Since(sendStart)
	t.Logf("send phase done: %d/%d accepted in %s (~%.1f rps actual)",
		sentOK.Load(), numEvents,
		sendElapsed.Round(time.Millisecond),
		float64(sentOK.Load())/sendElapsed.Seconds(),
	)

	// --- 6. Wait until all first-attempt deliveries arrive ------------------
	// We poll until the sum across all mocks reaches expectedFirstAttempts.
	// The count may exceed the expectation because 500 responses are retried.

	t.Logf("waiting for %d first-attempt deliveries (timeout %s)...",
		expectedFirstAttempts, waitTimeout)

	deadline := time.Now().Add(waitTimeout)
	for {
		var got int64
		for _, m := range mocks {
			got += m.total.Load()
		}
		if got >= expectedFirstAttempts {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout: received %d/%d deliveries", got, expectedFirstAttempts)
		}
		time.Sleep(500 * time.Millisecond)
	}

	// --- 7. Final report ----------------------------------------------------

	var totAll, tot200, tot400, tot500 int64
	for i, m := range mocks {
		d := m.total.Load()
		ok := m.ok200.Load()
		bad := m.fail400.Load()
		srv := m.fail500.Load()
		t.Logf("mock[%d]: total=%-6d  200=%-6d  400=%-6d  500=%d", i, d, ok, bad, srv)
		totAll += d
		tot200 += ok
		tot400 += bad
		tot500 += srv
	}

	// retries = total deliveries received − expected first attempts
	retries := totAll - expectedFirstAttempts
	t.Logf("summary rps=%d: events=%d sent_ok=%d | deliveries=%d 200=%d 400=%d 500=%d retries=%d",
		rps, numEvents, sentOK.Load(), totAll, tot200, tot400, tot500, retries)
}
