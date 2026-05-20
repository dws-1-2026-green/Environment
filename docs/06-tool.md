# Webhook Tool — ручное тестирование

> Используй этот инструмент для быстрой e2e проверки после деплоя.
> Для автоматизированных тестов — см. [03-testing.md](03-testing.md).

Консольная утилита для ручного тестирования всего pipeline: отправка событий → маршрутизация → доставка вебхука.

## Сборка

```bash
go build -o webhook-tool.exe ./cmd/webhook-tool
```

## Запуск

```bash
./webhook-tool.exe [flags]
```

| Флаг | По умолчанию | Описание |
|------|-------------|----------|
| `-url` | `http://event-receiver.localhost` | URL Event Receiver |
| `-source` | `manual-sender` | Имя источника событий |
| `-subs-api` | `http://subscriptions.localhost` | URL Subscriptions API |
| `-port` | `8888` | Локальный порт для приёма вебхука |

## Команды

### `subscribe` — подписаться на вебхук

```
subscribe <event_type> <source> [webhook_path]
```

Регистрирует подписку в Subscriptions API. Входящие вебхуки отображаются прямо в консоли.

```
> subscribe order.created manual-sender
✓ Subscribed to 'order.created' from source 'manual-sender'
  Webhook URL:     http://host-webhook-listener.webhooks.svc.cluster.local:8888/order.created
  Subscription ID: 550e8400-e29b-41d4-a716-446655440000
```

### `send` — отправить события

```
send <count> <event_type> <json_payload>
```

```
> send 1 order.created {"order_id":"123","amount":100}
[1/1] Sent: evt_a1b2c3d4e5f6  type=order.created
```

### `list` — список подписок текущего сеанса

```
> list
════════════════════════════════════════
  Subscriptions for instance a1b2c3d4
════════════════════════════════════════
ID:         550e8400-...
Event Type: order.created
```

## Типичный сценарий

```bash
# 1. Запустить утилиту
./webhook-tool.exe -source my-system

# 2. Подписаться
> subscribe order.created my-system

# 3. Отправить событие
> send 1 order.created {"order_id":"42","amount":999}

# 4. Убедиться что вебхук пришёл — утилита выведет:
╔════════════════════════════════════════╗
║         [WEBHOOK RECEIVED]             ║
╚════════════════════════════════════════╝
Type: order.created   Time: 2026-05-21T10:00:01Z
{"event_id": "evt_...", "order_id": "42", ...}
```

## Как работает приём вебхука

Утилита поднимает HTTP-сервер на `localhost:<port>`. Kubernetes получает доступ к нему через ExternalName-сервис `host-webhook-listener` (`k8s/overlays/local/host-listener.yaml`), который указывает на `host.docker.internal`.

Каждый запуск получает уникальный **instance ID** — он вшивается в подписку, что позволяет `list` показывать только «свои» подписки.
