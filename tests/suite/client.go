package suite

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// Client is a thin, auth-aware wrapper over the event-receiver and
// subscriptions APIs. All requests carry basic auth when configured.
type Client struct {
	cfg  *Config
	http *http.Client
}

// NewClient builds a Client from config.
func NewClient(cfg *Config) *Client {
	return &Client{
		cfg:  cfg,
		http: &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *Client) do(method, url string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.cfg.HasAuth() {
		req.SetBasicAuth(c.cfg.BasicAuthUser, c.cfg.BasicAuthPass)
	}
	return c.http.Do(req)
}

// doRetry is for subscription-management calls: it retries only on transport
// errors (DNS blips, refused connections), never on an HTTP status — so a
// transient network hiccup doesn't abort a test, while a real server response
// (incl. errors) is returned as-is and never duplicated. Not used by SendEvent,
// which must measure raw latency without retries.
func (c *Client) doRetry(method, url string, body []byte) (*http.Response, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		var rdr io.Reader
		if body != nil {
			rdr = bytes.NewReader(body)
		}
		resp, err := c.do(method, url, rdr)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		time.Sleep(time.Duration(attempt+1) * 200 * time.Millisecond)
	}
	return nil, fmt.Errorf("after retries: %w", lastErr)
}

// Health hits /health and returns an error if the receiver is not OK.
func (c *Client) Health() error {
	resp, err := c.doRetry("GET", c.cfg.EventReceiverURL+"/health", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health returned %d", resp.StatusCode)
	}
	return nil
}

// CreateSubscription registers a subscription and returns its id.
func (c *Client) CreateSubscription(source, eventType, destURL string) (string, error) {
	reqBody := map[string]any{
		"source":          source,
		"event_type":      eventType,
		"destination_url": destURL,
		"http_method":     "POST",
		"headers":         map[string]string{"Content-Type": "application/json"},
	}
	b, _ := json.Marshal(reqBody)
	resp, err := c.doRetry("POST", c.cfg.SubscriptionsURL+"/api/v1/subscriptions", b)
	if err != nil {
		return "", fmt.Errorf("create subscription: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("create subscription: status %d, body: %s", resp.StatusCode, string(body))
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode subscription response: %w", err)
	}
	return fmt.Sprintf("%v", out["subscription_id"]), nil
}

// CorrelationKey is the field injected into event.data so the webhook listener
// can match a received delivery (which carries only event.data) back to the
// event that produced it.
const CorrelationKey = "_corr"

// ListSubscriptionIDs returns the ids of subscriptions matching source (and
// optionally event_type). Used by cleanup to find everything a test created.
func (c *Client) ListSubscriptionIDs(source, eventType string) ([]string, error) {
	url := c.cfg.SubscriptionsURL + "/api/v1/subscriptions?source=" + source
	if eventType != "" {
		url += "&event_type=" + eventType
	}
	resp, err := c.doRetry("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("list subscriptions: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list subscriptions: status %d", resp.StatusCode)
	}
	var subs []struct {
		ID string `json:"subscription_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&subs); err != nil {
		return nil, fmt.Errorf("decode subscriptions: %w", err)
	}
	ids := make([]string, 0, len(subs))
	for _, s := range subs {
		ids = append(ids, s.ID)
	}
	return ids, nil
}

// DeleteSubscriptionsBySource removes every subscription registered for source.
// Returns how many were deleted. Best-effort: errors per id are ignored so a
// single failure does not leave the rest behind.
func (c *Client) DeleteSubscriptionsBySource(source string) (int, error) {
	ids, err := c.ListSubscriptionIDs(source, "")
	if err != nil {
		return 0, err
	}
	deleted := 0
	for _, id := range ids {
		if err := c.DeleteSubscription(id); err == nil {
			deleted++
		}
	}
	return deleted, nil
}

// UpdateSubscriptionDestination updates a subscription's destination_url via
// PUT /api/v1/subscriptions/{id}. Used to "change" a subscription while events
// are flowing.
func (c *Client) UpdateSubscriptionDestination(id, destURL string) error {
	body, _ := json.Marshal(map[string]any{"destination_url": destURL})
	resp, err := c.doRetry("PUT", c.cfg.SubscriptionsURL+"/api/v1/subscriptions/"+id, body)
	if err != nil {
		return fmt.Errorf("update subscription: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("update subscription: status %d", resp.StatusCode)
	}
	return nil
}

// DeleteSubscription removes a subscription via DELETE /api/v1/subscriptions/{id}.
func (c *Client) DeleteSubscription(id string) error {
	resp, err := c.doRetry("DELETE", c.cfg.SubscriptionsURL+"/api/v1/subscriptions/"+id, nil)
	if err != nil {
		return fmt.Errorf("delete subscription: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("delete subscription: status %d", resp.StatusCode)
	}
	return nil
}

// SendEvent publishes one event for source/eventType and returns the event id
// plus the HTTP status the receiver responded with.
//
// The event id is also injected into data[CorrelationKey] because delivery-service
// forwards only event.data as the webhook body — this is how the listener
// correlates deliveries back to events.
func (c *Client) SendEvent(source, eventType string, data map[string]any) (eventID string, status int, err error) {
	eventID = uuid.NewString()
	if data == nil {
		data = map[string]any{"order_id": uuid.NewString()[:8]}
	}
	data[CorrelationKey] = eventID
	payload := map[string]any{
		"id":         eventID,
		"type":       eventType,
		"created_at": time.Now().UTC().Format(time.RFC3339),
		"data":       data,
	}
	b, _ := json.Marshal(payload)
	url := fmt.Sprintf("%s/sources/%s/events", c.cfg.EventReceiverURL, source)
	resp, err := c.do("POST", url, bytes.NewBuffer(b))
	if err != nil {
		return eventID, 0, fmt.Errorf("send event: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return eventID, resp.StatusCode, nil
}
