#!/bin/sh
# Registers load-test subscriptions in k8s via Ingress.
# Destination URLs use k8s service DNS so delivery-service
# inside k8s can reach the mock pods deployed in the same cluster.
#
# Prerequisites:
#   1. k8s stack is running:  kubectl apply -k overlays/local
#   2. Mocks are deployed:    kubectl apply -f tests/k8s/mocks.yaml
#   3. Mock image is built:   docker build -t mock-receiver:local ./cmd/mock-receiver
#   4. hosts file contains:   127.0.0.1 subscriptions.localhost

set -e

SUBSCRIPTIONS_URL="${SUBSCRIPTIONS_URL:-http://subscriptions.localhost}"
NAMESPACE="${NAMESPACE:-webhooks}"

echo "waiting for subscriptions-api at $SUBSCRIPTIONS_URL..."
until curl -sf "$SUBSCRIPTIONS_URL/api/v1/subscriptions" > /dev/null 2>&1; do
  sleep 3
done
echo "subscriptions-api is ready"

curl -sf -X POST "$SUBSCRIPTIONS_URL/api/v1/subscriptions" \
  -H 'Content-Type: application/json' \
  -d "{\"source\":\"load-test\",\"event_type\":\"load.event\",\"destination_url\":\"http://mock-reliable.${NAMESPACE}.svc.cluster.local:8080\",\"http_method\":\"POST\",\"headers\":{}}" \
  && echo "registered -> mock-reliable (k8s service)"

curl -sf -X POST "$SUBSCRIPTIONS_URL/api/v1/subscriptions" \
  -H 'Content-Type: application/json' \
  -d "{\"source\":\"load-test\",\"event_type\":\"load.event\",\"destination_url\":\"http://mock-flaky.${NAMESPACE}.svc.cluster.local:8080\",\"http_method\":\"POST\",\"headers\":{}}" \
  && echo "registered -> mock-flaky   (k8s service)"

curl -sf -X POST "$SUBSCRIPTIONS_URL/api/v1/subscriptions" \
  -H 'Content-Type: application/json' \
  -d "{\"source\":\"load-test\",\"event_type\":\"load.event\",\"destination_url\":\"http://mock-chaos.${NAMESPACE}.svc.cluster.local:8080\",\"http_method\":\"POST\",\"headers\":{}}" \
  && echo "registered -> mock-chaos   (k8s service)"

echo "mock-setup done"
