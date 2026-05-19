package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type WebhookServer struct {
	mu          sync.RWMutex
	lastEvents  map[string][]byte
	httpServer  *http.Server
	listener    net.Listener
	httpClient  *http.Client
	subsBaseURL string
	localPort   string
	instanceID  string
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
	mux.HandleFunc("/", ws.handleWebhook)

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

func (ws *WebhookServer) Stop(ctx context.Context) error {
	if ws.httpServer != nil {
		return ws.httpServer.Shutdown(ctx)
	}
	return nil
}

func (ws *WebhookServer) handleWebhook(w http.ResponseWriter, r *http.Request) {
	eventType := strings.TrimPrefix(r.URL.Path, "/")
	if eventType == "" {
		eventType = r.Header.Get("X-Event-Type")
	}

	switch r.Method {
	case http.MethodGet:
		ws.mu.RLock()
		payload := ws.lastEvents[eventType]
		ws.mu.RUnlock()

		if len(payload) == 0 {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(payload)

	case http.MethodPost, http.MethodPut, http.MethodPatch:
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		pretty, _ := json.MarshalIndent(body, "", "  ")

		ws.mu.Lock()
		ws.lastEvents[eventType] = pretty
		ws.mu.Unlock()

		fmt.Printf("\n╔════════════════════════════════════════╗\n")
		fmt.Printf("║         [WEBHOOK RECEIVED]             ║\n")
		fmt.Printf("╚════════════════════════════════════════╝\n")
		fmt.Printf("Type: %s   Time: %s\n", eventType, time.Now().Format(time.RFC3339))
		fmt.Printf("%s\n\n", string(pretty))
		fmt.Print("> ")

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "received"})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}
