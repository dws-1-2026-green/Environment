package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

// Staging environment URLs and credentials.
// Override via env vars if needed.
func stagingEventReceiverURL() string {
	if v := os.Getenv("E2E_EVENT_RECEIVER_URL"); v != "" {
		return v
	}
	return "http://staging.dws.sidey383.ru"
}

func stagingSubscriptionsURL() string {
	if v := os.Getenv("E2E_SUBSCRIPTIONS_URL"); v != "" {
		return v
	}
	return "http://subscriptions.dws.sidey383.ru"
}

func stagingBasicAuthUser() string {
	if v := os.Getenv("E2E_BASIC_AUTH_USER"); v != "" {
		return v
	}
	return "admin"
}

func stagingBasicAuthPass() string {
	if v := os.Getenv("E2E_BASIC_AUTH_PASS"); v != "" {
		return v
	}
	return "tVbfSvowVeJXPqW3mvYf"
}

// newStagingRequest creates an HTTP request with basic auth pre-set.
func newStagingRequest(method, url string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(stagingBasicAuthUser(), stagingBasicAuthPass())
	return req, nil
}

func TestStagingHealthCheck(t *testing.T) {
	client := &http.Client{}
	req, err := newStagingRequest("GET", stagingEventReceiverURL()+"/health", nil)
	assert.NoError(t, err)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Event Receiver not reachable at %s: %v", stagingEventReceiverURL(), err)
	}
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var response map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&response)
	assert.NoError(t, err)
	assert.Equal(t, "OK", response["status"])

	t.Logf("Staging Event Receiver health check passed: %v", response)
}

func TestStagingEventReceiverPublishesEvent(t *testing.T) {
	client := &http.Client{}
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
	endpoint := fmt.Sprintf("%s/sources/%s/events", stagingEventReceiverURL(), sourceName)

	req, err := newStagingRequest("POST", endpoint, bytes.NewBuffer(payloadBytes))
	assert.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	assert.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var response map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&response)
	assert.NoError(t, err)
	assert.Equal(t, "OK", response["status"])

	t.Logf("Event %s successfully accepted by Staging Event Receiver", eventID)
}

func TestStagingSubscriptionsAPICRUD(t *testing.T) {
	client := &http.Client{}

	// Create a subscription
	subReq := map[string]interface{}{
		"source":          "staging-test-source",
		"event_type":      "test.staging.event",
		"destination_url": "http://example.com/webhook",
		"http_method":     "POST",
		"headers": map[string]string{
			"Content-Type": "application/json",
		},
	}

	subBytes, _ := json.Marshal(subReq)
	req, err := newStagingRequest("POST", stagingSubscriptionsURL()+"/api/v1/subscriptions", bytes.NewBuffer(subBytes))
	assert.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	assert.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	var subResponse map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&subResponse)
	subscriptionID, ok := subResponse["subscription_id"]
	assert.True(t, ok, "Response should contain subscription_id, got: %v", subResponse)
	t.Logf("Created subscription ID: %v", subscriptionID)

	// List subscriptions
	req2, err := newStagingRequest("GET", stagingSubscriptionsURL()+"/api/v1/subscriptions", nil)
	assert.NoError(t, err)
	resp2, err := client.Do(req2)
	assert.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusOK, resp2.StatusCode)

	var subs []interface{}
	json.NewDecoder(resp2.Body).Decode(&subs)
	t.Logf("Total subscriptions: %d", len(subs))
}

// TestStagingEndToEndWebhookDelivery tests the full pipeline on staging.
// NOTE: This test requires a publicly reachable webhook target URL.
// It uses a mock httptest server which is NOT reachable from staging delivery-service.
// Instead, we use a public echo service to verify delivery routing works.
// The test verifies the subscription + event acceptance parts work;
// actual delivery confirmation is not possible without a public endpoint.
func TestStagingEndToEndWebhookDelivery(t *testing.T) {
	client := &http.Client{}
	sourceName := "test-source-" + uuid.NewString()[:8]
	eventType := "order.created"

	// Use a public webhook testing service as target
	// (delivery may fail if the service rejects, but we verify the pipeline accepts the event)
	targetURL := "https://httpbin.org/post"

	// Create subscription
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
	req, err := newStagingRequest("POST", stagingSubscriptionsURL()+"/api/v1/subscriptions", bytes.NewBuffer(subBytes))
	assert.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	var subResponse map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&subResponse)
	resp.Body.Close()
	subscriptionID := subResponse["subscription_id"]
	t.Logf("Created subscription %v for source %s -> %s", subscriptionID, sourceName, targetURL)

	// Send event
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
	endpoint := fmt.Sprintf("%s/sources/%s/events", stagingEventReceiverURL(), sourceName)

	req2, err := newStagingRequest("POST", endpoint, bytes.NewBuffer(payloadBytes))
	assert.NoError(t, err)
	req2.Header.Set("Content-Type", "application/json")

	resp2, err := client.Do(req2)
	assert.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusOK, resp2.StatusCode)

	var eventResp map[string]interface{}
	json.NewDecoder(resp2.Body).Decode(&eventResp)
	t.Logf("Event %s accepted by Event Receiver: %v", eventID, eventResp)

	// Give the pipeline a moment to route and attempt delivery
	t.Log("Waiting 5 seconds for pipeline processing...")
	time.Sleep(5 * time.Second)
	t.Log("Event sent and accepted. Webhook delivery to external URL is not directly verifiable from this test.")
	t.Log("Check Kafka UI / Grafana for delivery confirmation.")
}
