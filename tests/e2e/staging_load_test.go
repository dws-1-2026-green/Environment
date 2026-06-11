package e2e

// Staging/demo load test — sends events at a fixed RPS for 5 minutes.
// No delivery checking; triggers HPA (CPU-based) scaling of event-receiver.
//
// Run:
//   E2E_EVENT_RECEIVER_URL=http://demo.dws.sidey383.ru \
//   E2E_SUBSCRIPTIONS_URL=http://subscriptions.demo.dws.sidey383.ru \
//   E2E_BASIC_AUTH_USER=admin E2E_BASIC_AUTH_PASS=... \
//   go test ./tests/e2e/... -v -run TestStagingLoad_50RPS -timeout 20m

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

const stagingLoadDuration = 5 * time.Minute

func TestStagingLoad_10RPS(t *testing.T)  { runStagingLoad(t, 10) }
func TestStagingLoad_50RPS(t *testing.T)  { runStagingLoad(t, 50) }
func TestStagingLoad_100RPS(t *testing.T) { runStagingLoad(t, 100) }
func TestStagingLoad_200RPS(t *testing.T) { runStagingLoad(t, 200) }
func TestStagingLoad_500RPS(t *testing.T) { runStagingLoad(t, 500) }

func runStagingLoad(t *testing.T, rps int) {
	t.Helper()

	hc := &http.Client{Timeout: 3 * time.Second}
	req, err := newStagingRequest("GET", stagingEventReceiverURL()+"/health", nil)
	if err != nil {
		t.Fatalf("build health request: %v", err)
	}
	if resp, err := hc.Do(req); err != nil {
		t.Fatalf("Event Receiver not reachable at %s: %v", stagingEventReceiverURL(), err)
	} else {
		resp.Body.Close()
	}

	source := "staging-load-" + uuid.NewString()[:8]
	t.Logf("starting load: rps=%d duration=%s source=%s target=%s",
		rps, stagingLoadDuration, source, stagingEventReceiverURL())

	var sent, ok, errCount atomic.Int64

	start := time.Now()
	deadline := start.Add(stagingLoadDuration)

	stopStats := make(chan struct{})
	go func() {
		tick := time.NewTicker(15 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-tick.C:
				elapsed := time.Since(start).Seconds()
				s := sent.Load()
				t.Logf("[%.0fs] sent=%d ok=%d err=%d actual=%.1f rps",
					elapsed, s, ok.Load(), errCount.Load(), float64(s)/elapsed)
			case <-stopStats:
				return
			}
		}
	}()

	client := &http.Client{Timeout: 5 * time.Second}
	ticker := time.NewTicker(time.Second / time.Duration(rps))
	defer ticker.Stop()

	var wg sync.WaitGroup

	for time.Now().Before(deadline) {
		<-ticker.C
		seq := sent.Add(1)
		wg.Add(1)
		go func(seq int64) {
			defer wg.Done()
			b, _ := json.Marshal(map[string]any{
				"id":         uuid.NewString(),
				"type":       "load.event",
				"created_at": time.Now().UTC().Format(time.RFC3339),
				"data":       map[string]any{"seq": seq},
			})
			r, _ := newStagingRequest("POST",
				fmt.Sprintf("%s/sources/%s/events", stagingEventReceiverURL(), source),
				bytes.NewBuffer(b))
			r.Header.Set("Content-Type", "application/json")

			resp, err := client.Do(r)
			if err != nil {
				errCount.Add(1)
				return
			}
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusAccepted {
				ok.Add(1)
			} else {
				errCount.Add(1)
			}
		}(seq)
	}

	wg.Wait()
	close(stopStats)

	elapsed := time.Since(start).Seconds()
	s := sent.Load()
	t.Logf("done: rps=%d duration=%.0fs sent=%d ok=%d err=%d actual=%.1f rps",
		rps, elapsed, s, ok.Load(), errCount.Load(), float64(s)/elapsed)
}
