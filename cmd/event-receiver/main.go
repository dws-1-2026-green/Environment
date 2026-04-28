package main

import (
	"bufio"
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
	"github.com/jackc/pgx/v5"
)

type WebhookServer struct {
	mu         sync.RWMutex
	lastEvents map[string][]byte // key: eventType, value: last received payload
	httpServer *http.Server
	listener   net.Listener
	dbConn     *pgx.Conn
	localPort  string
	localHost  string
	instanceID string
}

func NewWebhookServer(dbConn *pgx.Conn, localPort string) *WebhookServer {
	return &WebhookServer{
		lastEvents: make(map[string][]byte),
		dbConn:     dbConn,
		localPort:  localPort,
		localHost:  "localhost",
		instanceID: uuid.NewString()[:8],
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
	// Extract event type from path (e.g., /order.created)
	eventType := strings.TrimPrefix(r.URL.Path, "/")
	if eventType == "" {
		eventType = r.Header.Get("X-Event-Type")
	}

	// For GET requests, return last payload
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

	// For POST/PUT/PATCH, receive and store payload
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
	webhookURL := fmt.Sprintf("http://host.docker.internal:%s/%s", ws.localPort, webhookPath)

	// Create table if not exists
	_, err := ws.dbConn.Exec(ctx, `
		CREATE EXTENSION IF NOT EXISTS pgcrypto;
		CREATE TABLE IF NOT EXISTS subscriptions (
			id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
			source text NOT NULL,
			event_type text NOT NULL,
			target_url text NOT NULL,
			http_method text NOT NULL DEFAULT 'POST' CHECK (http_method IN ('POST', 'PUT', 'PATCH')),
			headers jsonb NOT NULL DEFAULT '{}'::jsonb,
			receiver_instance text,
			enabled boolean NOT NULL DEFAULT true,
			created_at timestamptz NOT NULL DEFAULT now()
		);
		CREATE INDEX IF NOT EXISTS idx_subscriptions_lookup 
			ON subscriptions (source, event_type) WHERE enabled = true;
	`)
	if err != nil {
		return fmt.Errorf("failed to create table: %w", err)
	}

	// Add receiver_instance column if it doesn't exist (migration)
	_, err = ws.dbConn.Exec(ctx, `
		ALTER TABLE subscriptions 
		ADD COLUMN IF NOT EXISTS receiver_instance text
	`)
	if err != nil {
		return fmt.Errorf("failed to migrate table: %w", err)
	}

	// Delete only subscriptions for this receiver instance
	_, err = ws.dbConn.Exec(ctx, 
		"DELETE FROM subscriptions WHERE source = $1 AND event_type = $2 AND receiver_instance = $3",
		sourceFilter, eventType, ws.instanceID)
	if err != nil {
		return fmt.Errorf("failed to delete existing subscription: %w", err)
	}

	// Insert new subscription
	var subID string
	err = ws.dbConn.QueryRow(ctx,
		`INSERT INTO subscriptions (source, event_type, target_url, http_method, headers, receiver_instance, enabled)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 RETURNING id`,
		sourceFilter, eventType, webhookURL, "POST", "{}", ws.instanceID, true).Scan(&subID)
	if err != nil {
		return fmt.Errorf("failed to insert subscription: %w", err)
	}

	fmt.Printf("✓ Subscribed to '%s' (source: %s)\n", eventType, sourceFilter)
	fmt.Printf("  Webhook URL: %s\n", webhookURL)
	fmt.Printf("  Subscription ID: %s\n", subID)
	fmt.Printf("  Instance: %s\n\n", ws.instanceID)
	return nil
}

func (ws *WebhookServer) ListSubscriptions(ctx context.Context) error {
	rows, err := ws.dbConn.Query(ctx,
		"SELECT event_type, source, target_url, receiver_instance, created_at FROM subscriptions WHERE enabled = true AND receiver_instance = $1 ORDER BY created_at DESC",
		ws.instanceID)
	if err != nil {
		return fmt.Errorf("failed to query subscriptions: %w", err)
	}
	defer rows.Close()

	fmt.Println("\n════════════════════════════════════════")
	fmt.Printf("      Subscriptions (Instance: %s)\n", ws.instanceID)
	fmt.Println("════════════════════════════════════════")

	count := 0
	for rows.Next() {
		var eventType, source, targetURL, instance string
		var createdAt time.Time
		if err := rows.Scan(&eventType, &source, &targetURL, &instance, &createdAt); err != nil {
			return err
		}
		fmt.Printf("Event Type: %s\n", eventType)
		fmt.Printf("Source: %s\n", source)
		fmt.Printf("Target URL: %s\n", targetURL)
		fmt.Printf("Created: %s\n\n", createdAt.Format(time.RFC3339))
		count++
	}

	if count == 0 {
		fmt.Println("No subscriptions found for this instance\n")
	}

	return rows.Err()
}

func (ws *WebhookServer) Stop(ctx context.Context) error {
	if ws.httpServer != nil {
		return ws.httpServer.Shutdown(ctx)
	}
	return nil
}

func main() {
	port := flag.String("port", "8888", "Port to listen on for webhooks")
	dbHost := flag.String("db-host", "localhost", "PostgreSQL host")
	dbPort := flag.Int("db-port", 5432, "PostgreSQL port")
	dbUser := flag.String("db-user", "green", "PostgreSQL user")
	dbPassword := flag.String("db-password", "green-password", "PostgreSQL password")
	dbName := flag.String("db-name", "green", "PostgreSQL database name")
	flag.Parse()

	ctx := context.Background()

	// Connect to database
	connStr := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable",
		*dbUser, *dbPassword, *dbHost, *dbPort, *dbName)
	
	conn, err := pgx.Connect(ctx, connStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to connect to database: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close(ctx)

	fmt.Printf("Connected to database at %s:%d\n\n", *dbHost, *dbPort)

	server := NewWebhookServer(conn, *port)

	// Start webhook server
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
				fmt.Println("Example: subscribe order.created test-system")
				fmt.Println("Example: subscribe order.created test-system custom/path")
				continue
			}

			eventType := parts[1]
			sourceFilter := parts[2]
			webhookPath := ""
			if len(parts) > 3 {
				webhookPath = strings.TrimSpace(parts[3])
			}

			err := server.Subscribe(ctx, eventType, sourceFilter, webhookPath)
			if err != nil {
				fmt.Printf("Error: %v\n", err)
				continue
			}

		case "list":
			err := server.ListSubscriptions(ctx)
			if err != nil {
				fmt.Printf("Error: %v\n\n", err)
			}

		default:
			fmt.Println("Unknown command. Use 'subscribe', 'list', or 'exit'")
		}
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "Error reading input: %v\n", err)
		os.Exit(1)
	}
}