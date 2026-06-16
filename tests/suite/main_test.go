package suite

import (
	"fmt"
	"os"
	"testing"
)

// Shared, package-wide fixtures initialised once in TestMain and reused by
// every case. Keeps a single webhook listener (one port) for the whole run.
var (
	cfg      *Config
	client   *Client
	listener *Listener
)

// TestMain wires up configuration, the API client and the webhook listener
// before running the case tests. It prints the effective configuration so a
// tester can see exactly what was targeted.
func TestMain(m *testing.M) {
	cfg = LoadConfig()
	client = NewClient(cfg)

	fmt.Println("──────────────────────────────────────────────")
	fmt.Println(" Functional test suite — effective configuration")
	fmt.Println("──────────────────────────────────────────────")
	for k, v := range cfg.Summary() {
		fmt.Printf("  %-20s %s\n", k, v)
	}
	fmt.Println("──────────────────────────────────────────────")

	// Preflight: is the event-receiver reachable?
	if err := client.Health(); err != nil {
		msg := fmt.Sprintf("event-receiver not reachable at %s: %v", cfg.EventReceiverURL, err)
		if cfg.SkipIfUnreachable {
			fmt.Println("SKIP: " + msg)
			os.Exit(0)
		}
		fmt.Println("FATAL: " + msg)
		os.Exit(1)
	}

	// Start the shared webhook listener.
	var err error
	listener, err = NewListener(cfg.ListenPort, cfg.CallbackBase())
	if err != nil {
		fmt.Printf("FATAL: cannot start webhook listener: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Webhook listener bound on :%s, advertised as %s\n\n", cfg.ListenPort, cfg.CallbackBase())

	code := m.Run()

	listener.Close()
	os.Exit(code)
}

// newSource returns a unique source name for an isolated test case.
func newSource(prefix string) string {
	return prefix + "-" + randToken()
}

// cleanupSource schedules deletion of every subscription created under source
// once the test finishes, so runs don't accumulate subscriptions on the target.
// Disable with E2E_CLEANUP=false (e.g. to inspect subscriptions after a run).
func cleanupSource(t *testing.T, source string) {
	if !envBool("E2E_CLEANUP", true) {
		return
	}
	t.Cleanup(func() {
		n, err := client.DeleteSubscriptionsBySource(source)
		if err != nil {
			t.Logf("%sCleanup: не удалось удалить подписки source=%s: %v", markerInfo, source, err)
			return
		}
		t.Logf("%sCleanup: удалено %d подписок (source=%s)", markerInfo, n, source)
	})
}
