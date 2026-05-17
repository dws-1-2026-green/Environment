package e2e

// Pure load tests — send events at a fixed RPS for 10 minutes, no delivery checking.
//
// Prerequisites (run before the test):
//   cd Environment && docker compose up -d
//   # create subscriptions for source "load-test" (or whatever STRESS_SOURCE is set to)
//
// Run:
//   go test -v -run TestLoad_10RPS  -timeout 15m
//   go test -v -run TestLoad_50RPS  -timeout 15m
//   go test -v -run TestLoad_100RPS -timeout 15m
//
// Env vars:
//   STRESS_SOURCE  — source name to send events to (default: "load-test")

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

const loadDuration = 10 * time.Minute

func TestLoad_10RPS(t *testing.T)  { runLoad(t, 10) }
func TestLoad_50RPS(t *testing.T)  { runLoad(t, 50) }
func TestLoad_100RPS(t *testing.T) { runLoad(t, 100) }

func runLoad(t *testing.T, rps int) {
	t.Helper()

	source := os.Getenv("STRESS_SOURCE")
	if source == "" {
		source = "load-test"
	}

	hc := &http.Client{Timeout: 3 * time.Second}
	if resp, err := hc.Get(eventReceiverURL + "/health"); err != nil {
		t.Skipf("EventReceiver not reachable: %v", err)
	} else {
		resp.Body.Close()
	}

	t.Logf("starting load: rps=%d duration=%s source=%s", rps, loadDuration, source)

	var sent, ok, errCount atomic.Int64

	start := time.Now()
	deadline := start.Add(loadDuration)

	// periodic stats log
	stopStats := make(chan struct{})
	go func() {
		tick := time.NewTicker(30 * time.Second)
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
		wg.Add(1)
		go func(seq int64) {
			defer wg.Done()
			b, _ := json.Marshal(map[string]any{
				"id":         uuid.NewString(),
				"type":       "load.event",
				"created_at": time.Now().UTC().Format(time.RFC3339),
				"data":       map[string]any{"seq": seq},
			})
			resp, err := client.Post(
				fmt.Sprintf("%s/sources/%s/events", eventReceiverURL, source),
				"application/json",
				bytes.NewBuffer(b),
			)
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
		}(sent.Add(1))
	}

	wg.Wait()
	close(stopStats)

	elapsed := time.Since(start).Seconds()
	s := sent.Load()
	t.Logf("done: rps=%d duration=%.0fs sent=%d ok=%d err=%d actual=%.1f rps",
		rps, elapsed, s, ok.Load(), errCount.Load(), float64(s)/elapsed)
}
