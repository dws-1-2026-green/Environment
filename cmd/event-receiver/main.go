package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type WebhookServer struct {
	mu           sync.RWMutex
	lastEvents   map[string][]byte // key: eventType, value: last received payload
	httpServer   *http.Server
	listener     net.Listener
	httpClient   *http.Client
	subsBaseURL  string
	localPort    string
	instanceID   string
}

func NewWebhookServer(subsBaseURL, localPort string) *WebhookServer {
	return &WebhookServer{
		lastEvents:  make(map[string][]byte),
		httpClient:  &http.Client{Timeout: 10 * time.Second},
		subsBaseURL: strings.TrimSuffix(subsBaseURL, "/"),
		localPort:   localPort,
		instanceID:  uuid.NewString()[:8],
	}
}

func (ws *WebhookServer) Start(addr string) error {
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		ws.handleWebhook(w, r)
	})

	var err error
	ws.listener, err = net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}

	ws.httpServer = &http.Server{
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	go func() {
		if err := ws.httpServer.Serve(ws.listener); err != nil && err != http.ErrServerClosed {
			fmt.Printf("Server error: %v\n", err)
		}
	}()

	return nil
}

func (ws *WebhookServer) handleWebhook(w http.ResponseWriter, r *http.Request) {
	eventType := strings.TrimPrefix(r.URL.Path, "/")
	if eventType == "" {
		eventType = r.Header.Get("X-Event-Type")
	}

	if r.Method == http.MethodGet {
		ws.mu.RLock()
		lastPayload := ws.lastEvents[eventType]
		ws.mu.RUnlock()

		if len(lastPayload) == 0 {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write(lastPayload)
		return
	}

	if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch {
		var payload map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		payloadBytes, _ := json.MarshalIndent(payload, "", "  ")

		ws.mu.Lock()
		ws.lastEvents[eventType] = payloadBytes
		ws.mu.Unlock()

		fmt.Printf("\n╔════════════════════════════════════════╗\n")
		fmt.Printf("║        [WEBHOOK RECEIVED]              ║\n")
		fmt.Printf("╚════════════════════════════════════════╝\n")
		fmt.Printf("Event Type: %s\n", eventType)
		fmt.Printf("Time: %s\n", time.Now().Format(time.RFC3339))
		fmt.Printf("Payload:\n%s\n\n", string(payloadBytes))
		fmt.Print("> ")

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "received"})
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func (ws *WebhookServer) Subscribe(ctx context.Context, eventType, sourceFilter, webhookPath string) error {
	if webhookPath == "" {
		webhookPath = eventType
	}
	webhookURL := fmt.Sprintf("http://host-webhook-listener.webhooks.svc.cluster.local:%s/%s", ws.localPort, webhookPath)

	reqBody := map[string]interface{}{
		"source":          sourceFilter,
		"event_type":      eventType,
		"destination_url": webhookURL,
		"http_method":     "POST",
		"headers": map[string]string{
			"X-Receiver-Instance": ws.instanceID,
		},
	}

	jsonBody, _ := json.Marshal(reqBody)
	resp, err := ws.httpClient.Post(
		fmt.Sprintf("%s/api/v1/subscriptions", ws.subsBaseURL),
		"application/json",
		bytes.NewBuffer(jsonBody),
	)
	if err != nil {
		return fmt.Errorf("failed to call subscriptions api: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("unexpected status from subscriptions api: %d", resp.StatusCode)
	}

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)

	fmt.Printf("✓ Subscribed to '%s' (source: %s)\n", eventType, sourceFilter)
	fmt.Printf("  Webhook URL: %s\n", webhookURL)
	fmt.Printf("  Subscription ID: %v\n", result["subscription_id"])
	fmt.Printf("  Instance: %s\n\n", ws.instanceID)
	return nil
}

func (ws *WebhookServer) ListSubscriptions(ctx context.Context) error {
	resp, err := ws.httpClient.Get(fmt.Sprintf("%s/api/v1/subscriptions", ws.subsBaseURL))
	if err != nil {
		return fmt.Errorf("failed to call subscriptions api: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status from subscriptions api: %d", resp.StatusCode)
	}

	var subs []map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&subs); err != nil {
		return fmt.Errorf("failed to decode subscriptions: %w", err)
	}

	fmt.Println("\n════════════════════════════════════════")
	fmt.Printf("      All Subscriptions (from API)\n")
	fmt.Println("════════════════════════════════════════")

	count := 0
	for _, sub := range subs {
		// Filter by instance in headers if we want to show only "ours"
		headers, ok := sub["headers"].(map[string]interface{})
		if ok && headers["X-Receiver-Instance"] != ws.instanceID {
			continue
		}

		fmt.Printf("ID: %v\n", sub["subscription_id"])
		fmt.Printf("Event Type: %v\n", sub["event_type"])
		fmt.Printf("Source: %v\n", sub["source"])
		fmt.Printf("Target URL: %v\n", sub["destination_url"])
		fmt.Printf("Created: %v\n\n", sub["created_at"])
		count++
	}

	if count == 0 {
		fmt.Println("No subscriptions found for this instance")
	}

	return nil
}

func (ws *WebhookServer) Stop(ctx context.Context) error {
	if ws.httpServer != nil {
		return ws.httpServer.Shutdown(ctx)
	}
	return nil
}

func main() {
	port := flag.String("port", "8888", "Port to listen on for webhooks")
	subsAPI := flag.String("subs-api", "http://subscriptions.localhost", "Subscriptions Service API URL")
	flag.Parse()

	server := NewWebhookServer(*subsAPI, *port)

	addr := fmt.Sprintf(":%s", *port)
	if err := server.Start(addr); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start server: %v\n", err)
		os.Exit(1)
	}

	localURL := fmt.Sprintf("http://localhost:%s", *port)

	fmt.Println("════════════════════════════════════════")
	fmt.Println("   Event Receiver (Subscriber) CLI")
	fmt.Println("════════════════════════════════════════")
	fmt.Printf("Listening on: %s\n", localURL)
	fmt.Printf("Subscriptions API: %s\n", *subsAPI)
	fmt.Printf("Instance ID: %s\n", server.instanceID)
	fmt.Println("\nCommands:")
	fmt.Println("  subscribe <event_type> <event_source> [webhook_path]")
	fmt.Println("  list")
	fmt.Println("  exit")
	fmt.Println("\nExamples:")
	fmt.Println("  subscribe order.created test-system")
	fmt.Println("  subscribe order.created test-system order/done")
	fmt.Println()

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			break
		}

		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}

		parts := strings.SplitN(input, " ", 4)
		if len(parts) == 0 {
			continue
		}

		command := parts[0]

		switch command {
		case "exit":
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			server.Stop(ctx)
			cancel()
			fmt.Println("Goodbye!")
			return

		case "subscribe":
			if len(parts) < 3 {
				fmt.Println("Usage: subscribe <event_type> <event_source> [webhook_path]")
				continue
			}

			eventType := parts[1]
			sourceFilter := parts[2]
			webhookPath := ""
			if len(parts) > 3 {
				webhookPath = strings.TrimSpace(parts[3])
			}

			err := server.Subscribe(context.Background(), eventType, sourceFilter, webhookPath)
			if err != nil {
				fmt.Printf("Error: %v\n", err)
			}

		case "list":
			err := server.ListSubscriptions(context.Background())
			if err != nil {
				fmt.Printf("Error: %v\n\n", err)
			}

		default:
			fmt.Println("Unknown command. Use 'subscribe', 'list', or 'exit'")
		}
	}
}