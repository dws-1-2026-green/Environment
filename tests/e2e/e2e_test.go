package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
)

// To run this test locally, ensure:
// 1. docker-compose up is running in green-spring-2026/Environment
// 2. The ports 8080 (EventReceiver), 5432 (Postgres) are accessible.

const (
	eventReceiverURL = "http://localhost:8080"
	postgresURL      = "postgres://green:green-password@localhost:5432/green?sslmode=disable"
)

func TestEndToEndWebhookDelivery(t *testing.T) {
	ctx := context.Background()

	// 1. Setup: Connect to Postgres to insert a subscription
	conn, err := pgx.Connect(ctx, postgresURL)
	if err != nil {
		t.Fatalf("Unable to connect to database: %v", err)
	}
	defer conn.Close(ctx)

	sourceName := "test-source-" + uuid.NewString()[:8]
	eventType := "order.created"

	// Create a mock server to receive the webhook from delivery-service
	receivedWebhookSignal := make(chan []byte, 1)
	mockTargetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		receivedWebhookSignal <- body
		w.WriteHeader(http.StatusOK)
	}))
	defer mockTargetServer.Close()

	// The delivery service runs inside Docker, so it needs to reach the host machine.
	// On Windows/Mac, 'host.docker.internal' usually works.
	u, _ := url.Parse(mockTargetServer.URL)
	targetURL := fmt.Sprintf("http://host.docker.internal:%s", u.Port())

	// Ensure table exists (migration might not have run in the test environment)
	_, err = conn.Exec(ctx, `
		create extension if not exists pgcrypto;
		create table if not exists subscriptions (
			id uuid primary key default gen_random_uuid (),
			source text not null,
			event_type text not null,
			target_url text not null,
			http_method text not null default 'POST' check (http_method in ('POST', 'PUT', 'PATCH')),
			headers jsonb not null default '{}'::jsonb,
			enabled boolean not null default true,
			created_at timestamptz not null default now ()
		);
		create index if not exists idx_subscriptions_lookup on subscriptions (source, event_type) where enabled = true;
	`)
	if err != nil {
		t.Fatalf("Failed to ensure subscriptions table exists: %v", err)
	}

	// Clean up and Insert Subscription
	_, err = conn.Exec(ctx, "DELETE FROM subscriptions WHERE source = $1", sourceName)
	assert.NoError(t, err)

	subscriptionID := uuid.New()
	_, err = conn.Exec(ctx,
		`INSERT INTO subscriptions (id, source, event_type, target_url, http_method, headers, enabled) 
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		subscriptionID, sourceName, eventType, targetURL, "POST", "{}", true,
	)
	if err != nil {
		t.Fatalf("Failed to insert subscription: %v", err)
	}
	t.Logf("Inserted subscription %s for source %s, target %s", subscriptionID, sourceName, targetURL)

	// Verify subscription was inserted correctly
	var checkURL string
	err = conn.QueryRow(ctx, "SELECT target_url FROM subscriptions WHERE id = $1", subscriptionID).Scan(&checkURL)
	if err != nil {
		t.Fatalf("Failed to verify subscription insertion: %v", err)
	}
	t.Logf("Verified subscription in DB with target_url: %s", checkURL)

	// 2. Action: Send event to EventReceiver
	eventID := uuid.NewString()
	eventPayload := map[string]interface{}{
		"id":   eventID,
		"type": eventType,
		"data": map[string]string{
			"order_id": "12345",
			"amount":   "100.00",
		},
	}

	payloadBytes, _ := json.Marshal(eventPayload)
	endpoint := fmt.Sprintf("%s/sources/%s/events", eventReceiverURL, sourceName)

	resp, err := http.Post(endpoint, "application/json", bytes.NewBuffer(payloadBytes))
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	t.Logf("Event sent to %s, response status: %d", endpoint, resp.StatusCode)

	// 3. Verification: Wait for delivery-service to call our mock server
	t.Log("Waiting for webhook delivery on mock server...")
	select {
	case receivedData := <-receivedWebhookSignal:
		t.Logf("Mock server received payload: %s", string(receivedData))
		// Verify the data received by the mock server matches what we sent
		var receivedEventData map[string]interface{}
		err := json.Unmarshal(receivedData, &receivedEventData)
		assert.NoError(t, err)

		// The delivery-service sends the 'data' part of the event as the payload
		assert.Equal(t, "12345", receivedEventData["order_id"])
		assert.Equal(t, "100.00", receivedEventData["amount"])

		t.Logf("Successfully received webhook for event %s", eventID)

	case <-time.After(90 * time.Second):
		// Check if there are any messages in the Kafka topics
		t.Log("Timed out waiting for webhook delivery.")
		t.Log("This might indicate:")
		t.Log("1. Kafka services are not running or not connected")
		t.Log("2. Event Receiver did not publish to 'routing.requests' topic")
		t.Log("3. Subscriptions service did not process the event or publish to 'deliveries.to_send'")
		t.Log("4. Delivery service did not process the message or could not reach the mock server URL")
		t.Log("5. There's a JSON field tag mismatch between services")
		t.Fatal("Webhook delivery timeout")
	}
}

func TestEventReceiverPublishesEvent(t *testing.T) {
	// This is a simpler test that just verifies the Event Receiver accepts the event
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

	body, _ := io.ReadAll(resp.Body)
	var response map[string]interface{}
	err = json.Unmarshal(body, &response)
	assert.NoError(t, err)
	assert.Equal(t, "OK", response["status"])

	t.Logf("Event %s successfully accepted by Event Receiver", eventID)
}

func TestHealthChecks(t *testing.T) {
	// Simple check to see if the event receiver is up
	resp, err := http.Get(eventReceiverURL + "/health")
	if err != nil {
		t.Skip("Event Receiver not reachable, skipping health check test")
		return
	}
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body, _ := io.ReadAll(resp.Body)
	var response map[string]interface{}
	err = json.Unmarshal(body, &response)
	assert.NoError(t, err)
	assert.Equal(t, "OK", response["status"])

	t.Log("Event Receiver health check passed")
}

func TestDatabaseConnectivity(t *testing.T) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, postgresURL)
	if err != nil {
		t.Fatalf("Unable to connect to database: %v", err)
	}
	defer conn.Close(ctx)

	// Test basic query
	var dbTime time.Time
	err = conn.QueryRow(ctx, "SELECT NOW()").Scan(&dbTime)
	assert.NoError(t, err)
	t.Logf("Database connectivity test passed, current time: %s", dbTime.String())
}