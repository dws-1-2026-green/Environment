package suite

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Delivery records a single inbound webhook call captured by the listener.
type Delivery struct {
	At     time.Time      // when the call was received
	Body   map[string]any // parsed event.data payload
	Corr   string         // correlation id extracted from data[CorrelationKey]
	Status int            // HTTP status the listener returned for this call
}

// Endpoint is one registered webhook target. delivery-service hits it via the
// listener's advertised base URL + "/" + Token. Behavior decides what status
// each attempt receives (used to simulate flaky/failing subscribers).
type Endpoint struct {
	Token    string
	Behavior func(attempt int) int // returns HTTP status for the n-th attempt (1-based)

	mu         sync.Mutex
	deliveries []Delivery
	signal     chan struct{}
}

// URL is the full destination_url to register for this endpoint.
func (e *Endpoint) URL(base string) string { return base + "/cb/" + e.Token }

// Count returns how many deliveries this endpoint has received so far.
func (e *Endpoint) Count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.deliveries)
}

// Deliveries returns a snapshot copy of all captured deliveries.
func (e *Endpoint) Deliveries() []Delivery {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Delivery, len(e.deliveries))
	copy(out, e.deliveries)
	return out
}

// WaitForCount blocks until the endpoint has received at least n deliveries or
// the timeout elapses. Returns the final count and whether the target was met.
func (e *Endpoint) WaitForCount(n int, timeout time.Duration) (int, bool) {
	deadline := time.Now().Add(timeout)
	for {
		if c := e.Count(); c >= n {
			return c, true
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return e.Count(), false
		}
		select {
		case <-e.signal:
		case <-time.After(remaining):
		}
	}
}

// HasCorrelation reports whether a delivery with the given correlation id arrived.
func (e *Endpoint) HasCorrelation(corr string) bool {
	for _, d := range e.Deliveries() {
		if d.Corr == corr {
			return true
		}
	}
	return false
}

// Listener is a single HTTP server that multiplexes many Endpoints by token.
// One listener serves an entire test run.
type Listener struct {
	base   string
	server *http.Server
	ln     net.Listener

	mu        sync.RWMutex
	endpoints map[string]*Endpoint
}

// NewListener binds listenPort and advertises advertisedBase to delivery-service.
func NewListener(listenPort, advertisedBase string) (*Listener, error) {
	ln, err := net.Listen("tcp", ":"+listenPort)
	if err != nil {
		return nil, fmt.Errorf("listen on :%s: %w", listenPort, err)
	}
	l := &Listener{
		base:      advertisedBase,
		ln:        ln,
		endpoints: make(map[string]*Endpoint),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/cb/", l.handle)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "ok")
	})
	l.server = &http.Server{Handler: mux}
	go l.server.Serve(ln)
	return l, nil
}

func (l *Listener) handle(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Path[len("/cb/"):]
	l.mu.RLock()
	ep := l.endpoints[token]
	l.mu.RUnlock()

	body, _ := io.ReadAll(r.Body)
	var data map[string]any
	json.Unmarshal(body, &data)

	corr := ""
	if data != nil {
		if v, ok := data[CorrelationKey].(string); ok {
			corr = v
		}
	}

	if ep == nil {
		// Unknown token — still 200 so we don't induce phantom retries.
		w.WriteHeader(http.StatusOK)
		return
	}

	ep.mu.Lock()
	attempt := len(ep.deliveries) + 1
	status := http.StatusOK
	if ep.Behavior != nil {
		status = ep.Behavior(attempt)
	}
	ep.deliveries = append(ep.deliveries, Delivery{
		At:     time.Now(),
		Body:   data,
		Corr:   corr,
		Status: status,
	})
	ep.mu.Unlock()

	select {
	case ep.signal <- struct{}{}:
	default:
	}

	w.WriteHeader(status)
}

// Register creates a new endpoint with the given behavior and returns it.
// behavior may be nil (always 200). Token is auto-generated.
func (l *Listener) Register(behavior func(attempt int) int) *Endpoint {
	ep := &Endpoint{
		Token:    uuid.NewString()[:12],
		Behavior: behavior,
		signal:   make(chan struct{}, 1024),
	}
	l.mu.Lock()
	l.endpoints[ep.Token] = ep
	l.mu.Unlock()
	return ep
}

// Base returns the advertised base URL.
func (l *Listener) Base() string { return l.base }

// Close shuts the listener down.
func (l *Listener) Close() error { return l.server.Close() }

// --- Behavior builders -----------------------------------------------------

// AlwaysOK returns 200 for every attempt.
func AlwaysOK() func(int) int { return func(int) int { return http.StatusOK } }

// AlwaysStatus returns a fixed status for every attempt (e.g. 400, 500).
func AlwaysStatus(code int) func(int) int { return func(int) int { return code } }

// FailNThenOK returns failCode for the first n attempts, then 200.
// Use to verify retry + eventual success.
func FailNThenOK(n, failCode int) func(int) int {
	return func(attempt int) int {
		if attempt <= n {
			return failCode
		}
		return http.StatusOK
	}
}
