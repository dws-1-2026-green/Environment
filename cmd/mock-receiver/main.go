package main

import (
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"
)

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

type weights struct {
	// cumulative upper bounds, roll in [0, 100)
	clientErrorUpto int // 400 — fast-fail, no retries
	errorUpto       int // 503 — triggers exponential-backoff retry
	resetUpto       int // connection reset — same as timeout, triggers retry
	slowUpto        int // 200 after 500ms-2s
	// rest: 200 after 1-50ms (fast)
}

func main() {
	name := os.Getenv("MOCK_NAME")
	if name == "" {
		name = "mock"
	}
	port := os.Getenv("MOCK_PORT")
	if port == "" {
		port = "8080"
	}

	clientErrorPct := envInt("MOCK_CLIENT_ERROR_PCT", 0)
	errorPct := envInt("MOCK_ERROR_PCT", 5)
	resetPct := envInt("MOCK_RESET_PCT", 5)
	slowPct := envInt("MOCK_SLOW_PCT", 20)
	w := weights{
		clientErrorUpto: clientErrorPct,
		errorUpto:       clientErrorPct + errorPct,
		resetUpto:       clientErrorPct + errorPct + resetPct,
		slowUpto:        clientErrorPct + errorPct + resetPct + slowPct,
	}

	slog.Info("mock receiver starting",
		"name", name, "port", port,
		"client_error%", clientErrorPct, "error%", errorPct,
		"reset%", resetPct, "slow%", slowPct, "fast%", 100-w.slowUpto,
	)

	http.HandleFunc("/", func(rw http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)

		roll := rand.IntN(100)
		start := time.Now()
		var kind string

		switch {
		case roll < w.clientErrorUpto:
			kind = "client-error"
			rw.WriteHeader(http.StatusBadRequest)

		case roll < w.errorUpto:
			kind = "error"
			time.Sleep(time.Duration(rand.IntN(50)) * time.Millisecond)
			rw.WriteHeader(http.StatusServiceUnavailable)

		case roll < w.resetUpto:
			// Abruptly close the connection — delivery-service sees a network
			// error and schedules a retry, equivalent to exceeding a timeout.
			kind = "reset"
			hj, ok := rw.(http.Hijacker)
			if !ok {
				rw.WriteHeader(http.StatusServiceUnavailable)
				break
			}
			conn, _, _ := hj.Hijack()
			if tc, ok := conn.(*net.TCPConn); ok {
				tc.SetLinger(0) // send RST instead of FIN — cleaner reset signal
			}
			conn.Close()

		case roll < w.slowUpto:
			kind = "slow"
			time.Sleep(time.Duration(500+rand.IntN(1500)) * time.Millisecond)
			rw.WriteHeader(http.StatusOK)

		default:
			kind = "fast"
			time.Sleep(time.Duration(1+rand.IntN(49)) * time.Millisecond)
			rw.WriteHeader(http.StatusOK)
		}

		slog.Info("handled",
			"mock", name,
			"kind", kind,
			"latency_ms", time.Since(start).Milliseconds(),
		)
	})

	if err := http.ListenAndServe(":"+port, nil); err != nil {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
}
