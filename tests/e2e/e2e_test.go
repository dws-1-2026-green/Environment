package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

// To run this test locally, ensure:
// 1. docker-compose up is running in green-spring-2026/Environment
// 2. The ports 8080 (EventReceiver), 8082 (Subscriptions API) are accessible.

const (
	eventReceiverURL    = "http://localhost:8080"
	subscriptionsAPIURL = "http://localhost:8082"
)

func TestEndToEndWebhookDelivery(t *testing.T) {
	sourceName := "test-source-" + uuid.NewString()[:8]
	eventType := "order.created"

	// 1. Setup: Mock server to receive the webhook from delivery-service
	receivedWebhookSignal := make(chan []byte, 1)
	mockTargetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		receivedWebhookSignal <- body
		w.WriteHeader(http.StatusOK)
	}))
	defer mockTargetServer.Close()

	// The delivery service runs inside Docker, so it needs to reach the host machine.
	u, _ := url.Parse(mockTargetServer.URL)
	targetURL := fmt.Sprintf("http://host.docker.internal:%s", u.Port())

	// 2. Setup: Create subscription via Subscriptions API
	subReq := map[string]interface{}{
		"source":          sourceName,
		"event_type":      eventType,
		"destination_url": targetURL,
		"http_method":     "POST",
		"headers": map[string]string{
			"Content-Type": "application/json",
		},
	}

	subBytes, _ := json.Marshal(subReq)
	resp, err := http.Post(subscriptionsAPIURL+"/api/v1/subscriptions", "application/json", bytes.NewBuffer(subBytes))
	assert.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	var subResponse map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&subResponse)
	subscriptionID := subResponse["subscription_id"]
	t.Logf("Created subscription %v for source %s, target %s", subscriptionID, sourceName, targetURL)

	// 3. Action: Send event to EventReceiver
	eventID := uuid.NewString()
	eventPayload := map[string]interface{}{
		"id":   eventID,
		"type": eventType,
		"data": map[string]interface{}{
			"order_id": "12345",
			"amount":   100.00,
		},
	}

	payloadBytes, _ := json.Marshal(eventPayload)
	endpoint := fmt.Sprintf("%s/sources/%s/events", eventReceiverURL, sourceName)

	resp, err = http.Post(endpoint, "application/json", bytes.NewBuffer(payloadBytes))
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	t.Logf("Event sent to %s, response status: %d", endpoint, resp.StatusCode)

	// 4. Verification: Wait for delivery-service to call our mock server
	t.Log("Waiting for webhook delivery on mock server...")
	select {
	case receivedData := <-receivedWebhookSignal:
		t.Logf("Mock server received payload: %s", string(receivedData))

		var receivedEventData map[string]interface{}
		err := json.Unmarshal(receivedData, &receivedEventData)
		assert.NoError(t, err)

		// The delivery-service sends the 'data' part of the event as the payload
		assert.Equal(t, "12345", receivedEventData["order_id"])
		// JSON unmarshals numbers as float64 by default
		assert.Equal(t, 100.0, receivedEventData["amount"])

		t.Logf("Successfully received webhook for event %s", eventID)

	case <-time.After(60 * time.Second):
		t.Fatal("Webhook delivery timeout")
	}
}

func TestEventReceiverPublishesEvent(t *testing.T) {
	sourceName := "test-source-" + uuid.NewString()[:8]
	eventType := "test.event"

	eventID := uuid.NewString()
	eventPayload := map[string]interface{}{
		"id":   eventID,
		"type": eventType,
		"data": map[string]string{
			"test_field": "test_value",
		},
	}

	payloadBytes, _ := json.Marshal(eventPayload)
	endpoint := fmt.Sprintf("%s/sources/%s/events", eventReceiverURL, sourceName)

	resp, err := http.Post(endpoint, "application/json", bytes.NewBuffer(payloadBytes))
	assert.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var response map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&response)
	assert.NoError(t, err)
	assert.Equal(t, "OK", response["status"])

	t.Logf("Event %s successfully accepted by Event Receiver", eventID)
}

func TestEndToEndWebhookDeliveryLoad(t *testing.T) {
	const (
		numEvents        = 50
		numSubscriptions = 3
	)

	sourceName := "test-source-" + uuid.NewString()[:8]
	eventType := "order.created"

	// 1. Setup: spin up mock target servers
	type mockServer struct {
		server *httptest.Server
		msgs   chan struct{}
	}

	mocks := make([]mockServer, numSubscriptions)
	for i := range mocks {
		ch := make(chan struct{}, numEvents)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.ReadAll(r.Body)
			ch <- struct{}{}
			w.WriteHeader(http.StatusOK)
		}))
		mocks[i] = mockServer{server: srv, msgs: ch}
	}
	defer func() {
		for _, m := range mocks {
			m.server.Close()
		}
	}()

	// 2. Setup: one subscription per mock server, all for the same (source, event_type)
	for i, m := range mocks {
		u, _ := url.Parse(m.server.URL)
		targetURL := fmt.Sprintf("http://host.docker.internal:%s", u.Port())

		subReq := map[string]interface{}{
			"source":          sourceName,
			"event_type":      eventType,
			"destination_url": targetURL,
			"http_method":     "POST",
			"headers":         map[string]string{"Content-Type": "application/json"},
		}
		subBytes, _ := json.Marshal(subReq)
		resp, err := http.Post(subscriptionsAPIURL+"/api/v1/subscriptions", "application/json", bytes.NewBuffer(subBytes))
		assert.NoError(t, err)
		assert.Equal(t, http.StatusCreated, resp.StatusCode)
		resp.Body.Close()
		t.Logf("Subscription %d created → %s", i+1, targetURL)
	}

	// 3. Action: send numEvents events sequentially
	for i := range numEvents {
		payload := map[string]interface{}{
			"id":   uuid.NewString(),
			"type": eventType,
			"data": map[string]interface{}{"order_id": fmt.Sprintf("order-%d", i)},
		}
		b, _ := json.Marshal(payload)
		resp, err := http.Post(
			fmt.Sprintf("%s/sources/%s/events", eventReceiverURL, sourceName),
			"application/json", bytes.NewBuffer(b),
		)
		assert.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		resp.Body.Close()
	}
	t.Logf("Sent %d events, expecting %d total webhook deliveries", numEvents, numEvents*numSubscriptions)

	// 4. Verification: each mock server must receive exactly numEvents webhooks.
	// Servers receive webhooks in parallel, so checking them sequentially is fine —
	// by the time server 1 is done, the others are already well ahead.
	timeoutAt := time.Now().Add(120 * time.Second)
	for i, m := range mocks {
		received := 0
		for received < numEvents {
			remaining := time.Until(timeoutAt)
			if remaining <= 0 {
				t.Fatalf("server %d: timeout, received %d/%d webhooks", i+1, received, numEvents)
			}
			select {
			case <-m.msgs:
				received++
			case <-time.After(remaining):
				t.Fatalf("server %d: timeout, received %d/%d webhooks", i+1, received, numEvents)
			}
		}
		t.Logf("Server %d: %d/%d webhooks received ✓", i+1, received, numEvents)
	}

	t.Logf("Load test passed: %d events × %d subscriptions = %d total webhooks delivered",
		numEvents, numSubscriptions, numEvents*numSubscriptions)
}

func TestHealthChecks(t *testing.T) {
	resp, err := http.Get(eventReceiverURL + "/health")
	if err != nil {
		t.Skip("Event Receiver not reachable, skipping health check test")
		return
	}
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var response map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&response)
	assert.NoError(t, err)
	assert.Equal(t, "OK", response["status"])

	t.Log("Event Receiver health check passed")
}
