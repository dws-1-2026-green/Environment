package suite

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds every knob the functional test suite needs. Everything is
// driven by environment variables so the same test binary (and Docker image)
// can be pointed at local docker-compose, an in-cluster deployment, or staging
// without recompiling.
//
// All variables share the E2E_ prefix. Defaults target the staging stand so a
// bare `go test ./tests/suite/...` does something useful out of the box.
type Config struct {
	// --- Targets -----------------------------------------------------------
	EventReceiverURL string // e.g. http://staging.dws.sidey383.ru
	SubscriptionsURL string // e.g. http://subscriptions.dws.sidey383.ru

	// --- Auth --------------------------------------------------------------
	BasicAuthUser string // empty => no basic auth sent
	BasicAuthPass string

	// --- Webhook callback (how delivery-service reaches THIS test) ---------
	// The suite starts one HTTP listener and registers subscriptions that
	// point back at it. CallbackHost:CallbackPort is the address advertised
	// to delivery-service (must be reachable from inside the cluster / network).
	// ListenPort is the port actually bound locally (defaults to CallbackPort).
	CallbackHost string
	CallbackPort string
	ListenPort   string

	// --- Acceptance contract ----------------------------------------------
	// HTTP status codes that mean "event accepted" by the event-receiver.
	// Local returns 200, staging returns 202 — both are accepted by default.
	AcceptStatuses []int

	// --- Timing / behaviour knobs -----------------------------------------
	DeliveryTimeout  time.Duration // how long to wait for a webhook to arrive
	RetryWaitTimeout time.Duration // how long to wait for retries to complete
	NoRetryWindow    time.Duration // window to confirm a 4xx did NOT retry
	MinRetryAttempts int           // min attempts expected when a target returns 5xx
	FailFastOn5xx    bool          // (informational) whether 5xx triggers retries

	// --- Reachability ------------------------------------------------------
	SkipIfUnreachable bool // if true, skip (not fail) when receiver is down
}

// LoadConfig reads configuration from the environment, applying defaults.
func LoadConfig() *Config {
	c := &Config{
		EventReceiverURL: env("E2E_EVENT_RECEIVER_URL", "http://staging.dws.sidey383.ru"),
		SubscriptionsURL: env("E2E_SUBSCRIPTIONS_URL", "http://subscriptions.dws.sidey383.ru"),
		BasicAuthUser:    env("E2E_BASIC_AUTH_USER", "admin"),
		BasicAuthPass:    env("E2E_BASIC_AUTH_PASS", ""),

		CallbackHost: env("E2E_CALLBACK_HOST", "host.docker.internal"),
		CallbackPort: env("E2E_CALLBACK_PORT", "8089"),
		ListenPort:   env("E2E_LISTEN_PORT", ""),

		AcceptStatuses: envIntList("E2E_ACCEPT_STATUSES", []int{200, 202}),

		DeliveryTimeout:  envDuration("E2E_DELIVERY_TIMEOUT", 60*time.Second),
		RetryWaitTimeout: envDuration("E2E_RETRY_WAIT_TIMEOUT", 90*time.Second),
		NoRetryWindow:    envDuration("E2E_NO_RETRY_WINDOW", 20*time.Second),
		MinRetryAttempts: envInt("E2E_MIN_RETRY_ATTEMPTS", 2),
		FailFastOn5xx:    envBool("E2E_FAIL_FAST_ON_5XX", false),

		SkipIfUnreachable: envBool("E2E_SKIP_IF_UNREACHABLE", false),
	}

	c.EventReceiverURL = strings.TrimSuffix(c.EventReceiverURL, "/")
	c.SubscriptionsURL = strings.TrimSuffix(c.SubscriptionsURL, "/")
	if c.ListenPort == "" {
		c.ListenPort = c.CallbackPort
	}
	return c
}

// CallbackBase is the advertised base URL handed to delivery-service.
func (c *Config) CallbackBase() string {
	return fmt.Sprintf("http://%s:%s", c.CallbackHost, c.CallbackPort)
}

// HasAuth reports whether basic auth credentials are configured.
func (c *Config) HasAuth() bool { return c.BasicAuthUser != "" }

// IsAccepted reports whether code is one of the configured accept statuses.
func (c *Config) IsAccepted(code int) bool {
	for _, s := range c.AcceptStatuses {
		if s == code {
			return true
		}
	}
	return false
}

// Summary renders the effective configuration for the report header.
func (c *Config) Summary() map[string]string {
	auth := "none"
	if c.HasAuth() {
		auth = c.BasicAuthUser + ":***"
	}
	return map[string]string{
		"event_receiver_url": c.EventReceiverURL,
		"subscriptions_url":  c.SubscriptionsURL,
		"basic_auth":         auth,
		"callback":           c.CallbackBase(),
		"listen_port":        c.ListenPort,
		"accept_statuses":    fmt.Sprint(c.AcceptStatuses),
		"delivery_timeout":   c.DeliveryTimeout.String(),
		"retry_wait_timeout": c.RetryWaitTimeout.String(),
		"no_retry_window":    c.NoRetryWindow.String(),
		"min_retry_attempts": strconv.Itoa(c.MinRetryAttempts),
	}
}

// --- env helpers -----------------------------------------------------------

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return def
}

func envFloat(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			return f
		}
	}
	return def
}

func envBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(strings.TrimSpace(v)); err == nil {
			return b
		}
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(strings.TrimSpace(v)); err == nil {
			return d
		}
	}
	return def
}

func envIntList(key string, def []int) []int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	var out []int
	for _, part := range strings.Split(v, ",") {
		if n, err := strconv.Atoi(strings.TrimSpace(part)); err == nil {
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		return def
	}
	return out
}
