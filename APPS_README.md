# Event Sender & Receiver CLI

Two interactive CLI applications for manual testing of the webhook delivery system.

## Prerequisites

- Go 1.21 or higher
- Docker Compose running (Event Receiver service, Kafka, PostgreSQL)
- Event Receiver service on `http://localhost:8080`

## Build

```powershell
ninja build-all
# or: go build -o bin/event-sender.exe ./cmd/event-sender && go build -o bin/event-receiver.exe ./cmd/event-receiver
```

## Run

**Terminal 1 - Event Receiver:**
```powershell
go run ./cmd/event-receiver/main.go -port 8888
# or: .\bin\event-receiver.exe -port 8888
```

**Terminal 2 - Event Sender:**
```powershell
go run ./cmd/event-sender/main.go -url http://localhost:8080 -source test-system
# or: .\bin\event-sender.exe -url http://localhost:8080 -source test-system
```

## Usage

**Event Receiver Commands:**
```
subscribe <event_type> <event_source> [webhook_path]  - Create subscription
list                                                  - Show all subscriptions
exit                                                  - Exit
```

Examples:
```
> subscribe order.created app1
> subscribe user.registered app2 users/webhook
> subscribe payment.processed app3 payments/callback/v2
> list
> exit
```

**Event Sender Commands:**
```
send <count> <event_type> <json_payload>  - Send events
exit                                      - Exit
```

Examples:
```
> send 1 order.created {"order_id":"123","amount":100}
> send 5 user.registered {"user_id":"456","email":"test@example.com"}
> exit
```

## Testing Workflow

1. **Terminal 1 - Start Event Receiver:**
   ```powershell
   go run ./cmd/event-receiver/main.go -port 8888
   ```

2. **Terminal 1 - Create subscriptions:**
   ```
   > subscribe order.created test-system
   ✓ Subscribed to 'order.created' (source: test-system)
     Webhook URL: http://host.docker.internal:8888/order.created
     Instance: 78725dd6
   
   > subscribe order.done test-system order/done
   ✓ Subscribed to 'order.done' (source: test-system)
     Webhook URL: http://host.docker.internal:8888/order/done
     Instance: 78725dd6
   ```

3. **Terminal 2 - Start Event Sender:**
   ```powershell
   go run ./cmd/event-sender/main.go -url http://localhost:8080 -source test-system
   ```

4. **Terminal 2 - Send event:**
   ```
   > send 1 order.created {"order_id":"ORD-001","amount":99.99}
   [1/1] Event sent: evt_a1b2c3d4e5f6 (type: order.created)
   ```

5. **Terminal 1 - Receive webhook:**
   ```
   ╔════════════════════════════════════════╗
   ║        [WEBHOOK RECEIVED]              ║
   ╚════════════════════════════════════════╝
   Event Type: order.created
   Time: 2026-04-29T00:13:30Z
   Payload:
   {
     "order_id": "ORD-001",
     "amount": 99.99
   }
   ```

## Multiple Receiver Instances

Each Event Receiver CLI instance is assigned a unique Instance ID. This allows multiple receivers to subscribe to the same event type without overwriting each other's subscriptions.

**Example with two receivers:**

Terminal 1 (Port 8888):
```powershell
go run ./cmd/event-receiver/main.go -port 8888
Instance ID: 78725dd6

> subscribe order.created app1
✓ Subscribed to 'order.created' (source: app1)
  Webhook URL: http://host.docker.internal:8888/order.created
  Instance: 78725dd6
```

Terminal 2 (Port 8889):
```powershell
go run ./cmd/event-receiver/main.go -port 8889
Instance ID: f64c6d9b

> subscribe order.created app1
✓ Subscribed to 'order.created' (source: app1)
  Webhook URL: http://host.docker.internal:8889/order.created
  Instance: f64c6d9b
```

Both subscriptions coexist in the database, each with its own instance ID and webhook URL. Events are delivered to both receivers independently.

## Webhook Path Configuration

The `subscribe` command accepts an optional third parameter to customize the webhook path:

- **Default behavior** (no path specified):
  ```
  subscribe order.created app1
  → Webhook URL: http://host.docker.internal:8888/order.created
  ```

- **Custom path** (path specified):
  ```
  subscribe order.completed app1 order/done
  → Webhook URL: http://host.docker.internal:8888/order/done
  ```

- **Deep nested path**:
  ```
  subscribe payment.processed app1 payments/callback/v2
  → Webhook URL: http://host.docker.internal:8888/payments/callback/v2
  ```

## Flags

**Event Sender:**
- `-url` Event Receiver service URL (default: http://localhost:8080)
- `-source` Event source name (default: manual-sender)

**Event Receiver:**
- `-port` Port to listen on (default: 8888)
- `-db-host` PostgreSQL host (default: localhost)
- `-db-port` PostgreSQL port (default: 5432)
- `-db-user` PostgreSQL user (default: green)
- `-db-password` PostgreSQL password (default: green-password)
- `-db-name` Database name (default: green)

## How It Works

1. Event Sender → sends events to Event Receiver service (localhost:8080)
2. Event Receiver Service → publishes to Kafka routing.requests topic
3. Subscriptions Service → reads from Kafka, matches subscriptions
4. Delivery Service → sends HTTP POST to webhook URL
5. Event Receiver CLI → receives webhook and prints payload

Event Receiver CLI creates subscriptions in the database automatically with the `subscribe` command. The webhook URL format is `http://host.docker.internal:PORT/webhook_path` where the path can be customized.

## Troubleshooting

**Webhook not received:**
- Verify Docker services running: `docker-compose logs`
- Verify Event Receiver CLI is listening: check port output
- Verify subscription created: run `list` command
- Check firewall allows port 8888

**Connection refused:**
- Verify Event Receiver service running on localhost:8080
- Verify Docker containers are up: `docker-compose ps`

**Database connection error:**
- Verify PostgreSQL running: `docker-compose logs postgres`
- Check database credentials match flags
