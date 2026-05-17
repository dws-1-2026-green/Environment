#!/bin/sh
set -e

echo 'waiting for subscriptions-api...'
until curl -sf http://subscriptions-api:8082/api/v1/subscriptions > /dev/null 2>&1; do
  sleep 3
done

curl -sf -X POST http://subscriptions-api:8082/api/v1/subscriptions \
  -H 'Content-Type: application/json' \
  -d '{"source":"load-test","event_type":"load.event","destination_url":"http://mock-reliable:8080","http_method":"POST","headers":{}}' \
  && echo 'registered -> mock-reliable'

curl -sf -X POST http://subscriptions-api:8082/api/v1/subscriptions \
  -H 'Content-Type: application/json' \
  -d '{"source":"load-test","event_type":"load.event","destination_url":"http://mock-flaky:8080","http_method":"POST","headers":{}}' \
  && echo 'registered -> mock-flaky'

curl -sf -X POST http://subscriptions-api:8082/api/v1/subscriptions \
  -H 'Content-Type: application/json' \
  -d '{"source":"load-test","event_type":"load.event","destination_url":"http://mock-chaos:8080","http_method":"POST","headers":{}}' \
  && echo 'registered -> mock-chaos'

echo 'mock-setup done'
