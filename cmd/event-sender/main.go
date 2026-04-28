package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	defaultEventReceiverURL = "http://localhost:8080"
)

type EventPayload struct {
	ID        string                 `json:"id"`
	Type      string                 `json:"type"`
	CreatedAt string                 `json:"created_at"`
	Data      map[string]interface{} `json:"data"`
}

type Sender struct {
	baseURL    string
	sourceName string
	httpClient *http.Client
}

func NewSender(baseURL, sourceName string) *Sender {
	return &Sender{
		baseURL:    baseURL,
		sourceName: sourceName,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (s *Sender) SendEvent(count int, eventType string, data map[string]interface{}) error {
	for i := 0; i < count; i++ {
		event := EventPayload{
			ID:        "evt_" + uuid.NewString()[:12],
			Type:      eventType,
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
			Data:      data,
		}

		payloadBytes, err := json.Marshal(event)
		if err != nil {
			return fmt.Errorf("failed to marshal event: %w", err)
		}

		endpoint := fmt.Sprintf("%s/sources/%s/events", s.baseURL, s.sourceName)
		resp, err := s.httpClient.Post(endpoint, "application/json", bytes.NewBuffer(payloadBytes))
		if err != nil {
			return fmt.Errorf("failed to send event: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
			return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		}

		fmt.Printf("[%d/%d] Event sent: %s (type: %s)\n", i+1, count, event.ID, eventType)

		if count > 1 && i < count-1 {
			time.Sleep(100 * time.Millisecond)
		}
	}

	return nil
}

func parsePayload(payloadStr string) (map[string]interface{}, error) {
	var data map[string]interface{}
	err := json.Unmarshal([]byte(payloadStr), &data)
	if err != nil {
		return nil, fmt.Errorf("invalid JSON payload: %w", err)
	}
	return data, nil
}

func main() {
	baseURL := flag.String("url", defaultEventReceiverURL, "Event Receiver base URL")
	source := flag.String("source", "manual-sender", "Event source name")
	flag.Parse()

	sender := NewSender(*baseURL, *source)

	fmt.Println("════════════════════════════════════════")
	fmt.Println("       Event Sender CLI")
	fmt.Println("════════════════════════════════════════")
	fmt.Printf("Connected to: %s\n", *baseURL)
	fmt.Printf("Source: %s\n\n", *source)
	fmt.Println("Commands:")
	fmt.Println("  send <count> <event_type> <json_payload>")
	fmt.Println("  exit")
	fmt.Println("\nExample:")
	fmt.Println(`  send 1 order.created {"order_id":"123","amount":100}`)
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
			fmt.Println("Goodbye!")
			return

		case "send":
			if len(parts) < 4 {
				fmt.Println("Usage: send <count> <event_type> <json_payload>")
				fmt.Println(`Example: send 1 order.created {"order_id":"123","amount":100}`)
				continue
			}

			countStr := parts[1]
			eventType := parts[2]
			payloadStr := parts[3]

			// Parse count
			count, err := strconv.Atoi(countStr)
			if err != nil || count <= 0 {
				fmt.Printf("Error: count must be a positive integer, got: %s\n", countStr)
				continue
			}

			// Parse payload
			data, err := parsePayload(payloadStr)
			if err != nil {
				fmt.Printf("Error: %v\n", err)
				continue
			}

			// Send events
			err = sender.SendEvent(count, eventType, data)
			if err != nil {
				fmt.Printf("Error: %v\n", err)
				continue
			}

		default:
			fmt.Println("Unknown command. Use 'send' or 'exit'")
		}
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "Error reading input: %v\n", err)
		os.Exit(1)
	}
}