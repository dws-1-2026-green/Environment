package e2e

// Staging/demo stress test — full pipeline verification with live webhook delivery.
// 5 mock servers bind on consecutive ports (E2E_WEBHOOK_PORT .. +4, default 8090-8094).
// Requires all 5 ports forwarded to this machine before running.
//
// Run:
//   E2E_EVENT_RECEIVER_URL=http://demo.dws.sidey383.ru \
//   E2E_SUBSCRIPTIONS_URL=http://subscriptions.demo.dws.sidey383.ru \
//   E2E_BASIC_AUTH_USER=admin E2E_BASIC_AUTH_PASS=... \
//   E2E_WEBHOOK_HOST=home.sidey383.ru E2E_WEBHOOK_PORT=8090 \
//   go test ./tests/e2e/... -v -run TestStagingStress_10RPS -timeout 30m

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

// stagingStressMock is a webhook receiver that randomly returns 200 (80%), 400 (10%), 500 (10%).
type stagingStressMock struct {
	server  *httptest.Server
	total   atomic.Int64
	ok200   atomic.Int64
	fail400 atomic.Int64
	fail500 atomic.Int64
}

func newStagingStressMock(port int) (*stagingStressMock, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return nil, fmt.Errorf("listen :%d: %w", port, err)
	}
	m := &stagingStressMock{}
	m.server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.ReadAll(r.Body)
		m.total.Add(1)
		switch rand.IntN(10) {
		case 0:
			m.fail400.Add(1)
			w.WriteHeader(http.StatusBadRequest)
		case 1:
			m.fail500.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
		default:
			m.ok200.Add(1)
			w.WriteHeader(http.StatusOK)
		}
	}))
	m.server.Listener = ln
	m.server.Start()
	return m, nil
}

func (m *stagingStressMock) publicURL(host string, port int) string {
	return fmt.Sprintf("http://%s:%d", host, port)
}

// stagingStressProfile binds a unique (source, eventType) to a set of mock indexes.
type stagingStressProfile struct {
	source    string
	eventType string
	mockIdxs  []int
}

func TestStagingStress_10RPS(t *testing.T)  { runStagingStress(t, 10, 150, 4*time.Minute) }
func TestStagingStress_50RPS(t *testing.T)  { runStagingStress(t, 50, 750, 15*time.Minute) }
func TestStagingStress_100RPS(t *testing.T) { runStagingStress(t, 100, 1500, 25*time.Minute) }

func TestStagingStress_200RPS(t *testing.T) { runStagingStress(t, 200, 3000, 25*time.Minute) }

func TestStagingStress_500RPS(t *testing.T) { runStagingStress(t, 500, 7500, 30*time.Minute) }

func runStagingStress(t *testing.T, rps, numEvents int, waitTimeout time.Duration) {
	t.Helper()

	host := stagingWebhookCallbackHost()
	basePortStr := stagingWebhookCallbackPort()
	basePort, err := strconv.Atoi(basePortStr)
	if err != nil {
		t.Fatalf("invalid E2E_WEBHOOK_PORT %q: %v", basePortStr, err)
	}

	// Health check.
	hc := &http.Client{Timeout: 3 * time.Second}
	req, _ := newStagingRequest("GET", stagingEventReceiverURL()+"/health", nil)
	if resp, err := hc.Do(req); err != nil {
		t.Fatalf("Event Receiver not reachable: %v", err)
	} else {
		resp.Body.Close()
	}

	// Spin up 5 mock servers on consecutive ports.
	const numMocks = 5
	mocks := make([]*stagingStressMock, numMocks)
	for i := range mocks {
		port := basePort + i
		m, err := newStagingStressMock(port)
		if err != nil {
			t.Fatalf("mock[%d]: %v — open ports %d-%d first", i, err, basePort, basePort+numMocks-1)
		}
		mocks[i] = m
		t.Logf("mock[%d] listening on %s", i, m.publicURL(host, port))
	}
	defer func() {
		for _, m := range mocks {
			m.server.Close()
		}
	}()

	// Subscription topology (same fan-out structure as local stress test).
	profiles := []stagingStressProfile{
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
			mockIdxs:  []int{4, 0}, // 2 webhooks per event; mock[0] shared on purpose
		},
	}

	// Register subscriptions (generous timeout — cluster may be warming up).
	client := &http.Client{Timeout: 30 * time.Second}
	for _, p := range profiles {
		for _, idx := range p.mockIdxs {
			targetURL := mocks[idx].publicURL(host, basePort+idx)
			subReq := map[string]interface{}{
				"source":          p.source,
				"event_type":      p.eventType,
				"destination_url": targetURL,
				"http_method":     "POST",
				"headers":         map[string]string{"Content-Type": "application/json"},
			}
			b, _ := json.Marshal(subReq)
			r, _ := newStagingRequest("POST", stagingSubscriptionsURL()+"/api/v1/subscriptions", bytes.NewBuffer(b))
			r.Header.Set("Content-Type", "application/json")
			resp, err := client.Do(r)
			if err != nil {
				t.Fatalf("create subscription for mock[%d]: %v", idx, err)
			}
			if resp.StatusCode != http.StatusCreated {
				t.Fatalf("create subscription for mock[%d]: unexpected status %d", idx, resp.StatusCode)
			}
			var body map[string]interface{}
			json.NewDecoder(resp.Body).Decode(&body)
			resp.Body.Close()
			t.Logf("sub %-38v  %s → mock[%d] %s", body["subscription_id"], p.source, idx, targetURL)
		}
	}

	// Pre-compute expected first-attempt deliveries.
	var expectedFirstAttempts int64
	for i := range numEvents {
		expectedFirstAttempts += int64(len(profiles[i%len(profiles)].mockIdxs))
	}
	t.Logf("plan: %d events @ ~%d rps → %d expected first-attempt deliveries (avg fan-out %.1f)",
		numEvents, rps, expectedFirstAttempts, float64(expectedFirstAttempts)/float64(numEvents))

	// Send events at target RPS.
	sendStart := time.Now()
	ticker := time.NewTicker(time.Second / time.Duration(rps))
	defer ticker.Stop()

	var sentOK atomic.Int64
	var wg sync.WaitGroup

	for i := range numEvents {
		<-ticker.C
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
			r, _ := newStagingRequest("POST",
				fmt.Sprintf("%s/sources/%s/events", stagingEventReceiverURL(), p.source),
				bytes.NewBuffer(b))
			r.Header.Set("Content-Type", "application/json")
			resp, err := client.Do(r)
			if err != nil {
				return
			}
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusAccepted {
				sentOK.Add(1)
			}
		}(i)
	}
	wg.Wait()

	sendElapsed := time.Since(sendStart)
	t.Logf("send phase done: %d/%d accepted in %s (~%.1f rps actual)",
		sentOK.Load(), numEvents,
		sendElapsed.Round(time.Millisecond),
		float64(sentOK.Load())/sendElapsed.Seconds())

	// Wait until all first-attempt deliveries arrive.
	t.Logf("waiting for %d first-attempt deliveries (timeout %s)...", expectedFirstAttempts, waitTimeout)

	deadline := time.Now().Add(waitTimeout)
	reportTick := time.NewTicker(15 * time.Second)
	defer reportTick.Stop()

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
		select {
		case <-reportTick.C:
			t.Logf("progress: %d/%d deliveries received", got, expectedFirstAttempts)
		default:
			time.Sleep(500 * time.Millisecond)
		}
	}

	// Final report.
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

	retries := totAll - expectedFirstAttempts
	t.Logf("summary rps=%d: events=%d sent_ok=%d | deliveries=%d 200=%d 400=%d 500=%d retries=%d",
		rps, numEvents, sentOK.Load(), totAll, tot200, tot400, tot500, retries)
}
