package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
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
	endpoint := fmt.Sprintf("%s/sources/%s/events", s.baseURL, s.sourceName)

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

		resp, err := s.httpClient.Post(endpoint, "application/json", bytes.NewBuffer(payloadBytes))
		if err != nil {
			return fmt.Errorf("failed to send event: %w", err)
		}
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
			return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		}

		fmt.Printf("[%d/%d] Sent: %s  type=%s\n", i+1, count, event.ID, eventType)

		if i < count-1 {
			time.Sleep(100 * time.Millisecond)
		}
	}

	return nil
}

func parsePayload(payloadStr string) (map[string]interface{}, error) {
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(payloadStr), &data); err != nil {
		return nil, fmt.Errorf("invalid JSON payload: %w", err)
	}
	return data, nil
}
