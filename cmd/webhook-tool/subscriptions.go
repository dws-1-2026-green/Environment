package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
)

func (ws *WebhookServer) Subscribe(ctx context.Context, eventType, sourceFilter, webhookPath string) error {
	if webhookPath == "" {
		webhookPath = eventType
	}
	webhookURL := fmt.Sprintf(
		"http://host-webhook-listener.webhooks.svc.cluster.local:%s/%s",
		ws.localPort, webhookPath,
	)

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

	if resp.StatusCode != 201 {
		return fmt.Errorf("unexpected status from subscriptions api: %d", resp.StatusCode)
	}

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)

	fmt.Printf("✓ Subscribed to '%s' from source '%s'\n", eventType, sourceFilter)
	fmt.Printf("  Webhook URL:     %s\n", webhookURL)
	fmt.Printf("  Subscription ID: %v\n", result["subscription_id"])
	fmt.Printf("  Instance:        %s\n\n", ws.instanceID)
	return nil
}

func (ws *WebhookServer) ListSubscriptions(ctx context.Context) error {
	resp, err := ws.httpClient.Get(fmt.Sprintf("%s/api/v1/subscriptions", ws.subsBaseURL))
	if err != nil {
		return fmt.Errorf("failed to call subscriptions api: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("unexpected status from subscriptions api: %d", resp.StatusCode)
	}

	var subs []map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&subs); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	fmt.Printf("\n════════════════════════════════════════\n")
	fmt.Printf("  Subscriptions for instance %s\n", ws.instanceID)
	fmt.Printf("════════════════════════════════════════\n")

	count := 0
	for _, sub := range subs {
		if headers, ok := sub["headers"].(map[string]interface{}); ok {
			if headers["X-Receiver-Instance"] != ws.instanceID {
				continue
			}
		}
		fmt.Printf("ID:         %v\n", sub["subscription_id"])
		fmt.Printf("Event Type: %v\n", sub["event_type"])
		fmt.Printf("Source:     %v\n", sub["source"])
		fmt.Printf("Target URL: %v\n", sub["destination_url"])
		fmt.Printf("Created:    %v\n\n", sub["created_at"])
		count++
	}

	if count == 0 {
		fmt.Println("No subscriptions found for this instance")
	}

	return nil
}
