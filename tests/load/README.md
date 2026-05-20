# WebHook Engine Load Testing

This directory contains the load testing suite for the distributed WebHook delivery engine. The tests are designed to evaluate the performance, scalability, and reliability of the `EventReceiver` component and the downstream pipeline (Kafka, Delivery Service).

## Tooling: k6

We use [k6 by Grafana](https://k6.io/) for load testing. k6 is a modern, developer-centric tool written in Go that allows for high-performance testing with scripts written in JavaScript.

### Why k6?
- **Efficiency:** Low resource footprint compared to JMeter or Locust.
- **Protocol Support:** Excellent HTTP/1.1 and HTTP/2 support.
- **Thresholds:** Built-in support for "Pass/Fail" criteria based on performance metrics (e.g., p99 latency).

## Test Scenarios

The `webhook_load_test.js` script implements three distinct scenarios to stress the system differently:

### 1. Smoke Test
- **Goal:** Verify system health and baseline functionality.
- **Load:** Constant 10 Requests Per Second (RPS) for 30 seconds.
- **Purpose:** Ensure the API can communicate with Kafka and return 200 OK without errors under light load.

### 2. Stress Test (Ramping Arrival Rate)
- **Goal:** Find the system's "Knee of the Curve" and breaking point.
- **Load:** Ramps from 20 RPS to 400 RPS over 4 minutes.
- **Focus:** 
    - **Kafka Producer Throughput:** Observe if the `EventReceiver` starts returning 500 errors when Kafka buffers are full.
    - **Connection Pooling:** Test how the Go HTTP server handles increasing concurrent connections.

### 3. Concurrency Test
- **Goal:** Test goroutine efficiency and connection management.
- **Load:** 100 constant Virtual Users (VUs) hammering the system for 1 minute.
- **Focus:** Identifying resource contention or locking issues in the event ingestion path.

## How to Run

### Prerequisites
- The environment must be running (e.g., `docker-compose up -d`).
- The `EventReceiver` should be accessible at `http://localhost:8080` (default).

### Windows (Batch Script)
```powershell
# Run with default settings (targets localhost:8080)
.\run-load-tests.bat

# Run against a specific environment
.\run-load-tests.bat http://api-server.internal:8080
```

### Unix / Linux / macOS (Shell)
If you are on a Unix-like system, you can run k6 directly from the `Environment` root:
```bash
# Using local k6 installation
k6 run -e BASE_URL=http://localhost:8080 tests/load/webhook_load_test.js

# Using Docker
docker run --rm -i grafana/k6 run -e BASE_URL=http://host.docker.internal:8080 - < tests/load/webhook_load_test.js

> **Note for Linux users:** `host.docker.internal` may not resolve by default on Linux. You can add `--add-host=host.docker.internal:host-gateway` to the `docker run` command to enable it.
```

### Using k6 Directly
If you have k6 installed:
```bash
k6 run -e BASE_URL=http://localhost:8080 tests/load/webhook_load_test.js
```

### Running via Docker
If you don't have k6 installed:
```bash
docker run --rm -i grafana/k6 run -e BASE_URL=http://host.docker.internal:8080 - < tests/load/webhook_load_test.js
```

## Key Metrics to Observe

| Metric | Target | Description |
| :--- | :--- | :--- |
| `http_req_duration` (p95) | < 200ms | The time it takes for the API to receive the event and hand it off to Kafka. |
| `http_req_failed` | < 1% | The percentage of failed requests. 500 errors usually indicate Kafka producer failure or backpressure. |
| `Kafka Lag` | < 1000 | Monitor via `kafka-ui` (port 8081). High lag means the `Delivery Service` is slower than the ingestion. |

## Thresholds

The tests are configured with automatic thresholds. If the p99 latency exceeds 500ms or the error rate exceeds 1%, k6 will exit with a non-zero status code, making it suitable for CI/CD integration.

## Troubleshooting & E2E Verification

### Missing Events in CLI Receiver?
If you are running the `event-receiver` CLI (port 8888) and don't see logs during a load test:

1.  **Check Subscriptions:** The `k6` script sends events with specific sources and types. You must subscribe to them in the CLI first:
    *   `subscribe order.created ecommerce-app`
    *   `subscribe payment.success payment-gateway`
2.  **Verify Routing:** The `Subscriptions` service only forwards events that match an active subscription in the database.
3.  **Monitor Kafka Lag:** Check `http://localhost:8081`. If the API (8080) is fast but the CLI (8888) is quiet, the events are likely queued in Kafka because the `DeliveryService` is catching up.

### Performance Warnings
*   **Terminal Freeze:** High-volume load tests (400+ RPS) will produce a massive amount of output if your CLI receiver is subscribed. Standard Windows terminals (CMD/PowerShell) may hang or lag significantly when rendering this much text.
*   **Log Level:** If you don't see "Receive event" logs on the `EventReceiver` API side, ensure its log level is set to `INFO`.
```
