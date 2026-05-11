# Webhook Testing Tool

Консольная утилита для ручного тестирования всего pipeline:
отправка событий → маршрутизация → доставка webhook.

## Сборка

```bash
go build -o webhook-tool ./cmd/webhook-tool
```

## Запуск

```bash
./webhook-tool [flags]
```

| Флаг | По умолчанию | Описание |
|---|---|---|
| `-url` | `http://event-receiver.localhost` | URL Event Receiver |
| `-source` | `manual-sender` | Имя источника событий |
| `-subs-api` | `http://subscriptions.localhost` | URL Subscriptions API |
| `-port` | `8888` | Локальный порт для приёма webhook |

## Команды

### `send` — отправить события

```
send <count> <event_type> <json_payload>
```

```
> send 1 order.created {"order_id":"123","amount":100}
[1/1] Sent: evt_a1b2c3d4e5f6  type=order.created

> send 5 order.updated {"status":"shipped"}
[1/5] Sent: ...
...
```

### `subscribe` — подписаться на webhook

Регистрирует подписку в Subscriptions API. Входящие вебхуки будут отображаться
прямо в консоли при получении.

```
subscribe <event_type> <source> [webhook_path]
```

```
> subscribe order.created manual-sender
✓ Subscribed to 'order.created' from source 'manual-sender'
  Webhook URL:     http://host-webhook-listener.webhooks.svc.cluster.local:8888/order.created
  Subscription ID: 550e8400-e29b-41d4-a716-446655440000
  Instance:        a1b2c3d4
```

`webhook_path` — опциональный путь назначения (по умолчанию равен `event_type`).

### `list` — список подписок

Показывает только подписки текущего запуска (фильтр по instance ID).

```
> list
════════════════════════════════════════
  Subscriptions for instance a1b2c3d4
════════════════════════════════════════
ID:         550e8400-...
Event Type: order.created
Source:     manual-sender
Target URL: http://host-webhook-listener...
Created:    2024-01-01T12:00:00Z
```

## Типичный сценарий E2E-теста

```
# 1. Запустить утилиту
./webhook-tool -source my-system

# 2. Подписаться на нужный тип событий
> subscribe order.created my-system

# 3. Отправить событие
> send 1 order.created {"order_id":"42","amount":999}

# 4. Убедиться, что webhook пришёл — утилита выведет в консоль:
╔════════════════════════════════════════╗
║         [WEBHOOK RECEIVED]             ║
╚════════════════════════════════════════╝
Type: order.created   Time: 2024-01-01T12:00:01Z
{
  "event_id": "evt_...",
  "order_id": "42",
  ...
}
```

## Как работает приём webhook

При запуске утилита поднимает HTTP-сервер на `localhost:<port>`.
Kubernetes-кластер доступа к этому порту получает через ExternalName-сервис
`host-webhook-listener` (задан в `k8s/overlays/local/host-listener.yaml`),
который указывает на `host.docker.internal`.

Каждый запуск утилиты получает уникальный **instance ID** — он вшивается
в заголовок `X-Receiver-Instance` каждой подписки, что позволяет
при `list` показывать только «свои» подписки.
