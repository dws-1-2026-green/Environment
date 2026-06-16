# Staging окружение

> Связанные документы: [02-deploy.md](02-deploy.md) — развернуть своё окружение.

Тестовый стенд на одном сервере с Kubernetes.

## Доступ к сервисам

Все сервисы защищены Basic Auth.

| Сервис | URL |
|--------|-----|
| Event Receiver | http://staging.dws.sidey383.ru |
| Subscriptions API | http://subscriptions.staging.dws.sidey383.ru |
| Kafka UI | http://kafka-ui.staging.dws.sidey383.ru |
| Grafana | http://grafana.staging.dws.sidey383.ru |
| Prometheus | http://prometheus.staging.dws.sidey383.ru |
| Delivery Dashboard | http://delivery-dashboard.dws.sidey383.ru |

**Basic Auth и пароль Grafana:** см. у тимлида.

## Запуск тестов против staging

Обзор — [03-testing.md](03-testing.md), полный список env — [tests/README.md](../tests/README.md).

```powershell
$env:E2E_EVENT_RECEIVER_URL = "http://staging.dws.sidey383.ru"
$env:E2E_SUBSCRIPTIONS_URL  = "http://subscriptions.staging.dws.sidey383.ru"
$env:E2E_BASIC_AUTH_USER    = "admin"
$env:E2E_BASIC_AUTH_PASS    = "..."
$env:E2E_CALLBACK_HOST      = "<хост, достижимый из кластера>"
$env:E2E_CALLBACK_PORT      = "8089"
go test -json ./tests/suite/... | go run ./cmd/report-gen -out functional.html
```

> Функциональные кейсы поднимают приёмник вебхуков и регистрируют подписки,
> указывающие на `E2E_CALLBACK_HOST:E2E_CALLBACK_PORT` — он должен быть достижим
> со стороны delivery-service.

## Что запущено в кластере

| Pod | Описание |
|-----|----------|
| `event-receiver` | HTTP API приёма событий |
| `subscriptions-api` | CRUD API подписок |
| `subscriptions-worker` | Kafka consumer, маршрутизация |
| `delivery-service` | Доставка вебхуков |
| `delivery-dashboard` | UI статусов доставок |
| `kafka-dual-role-0` | Брокер сообщений (Strimzi) |
| `cassandra-dc1-default-sts-0` | БД подписок (k8ssandra) |
| `delivery-postgres-0` | PostgreSQL — статусы доставок |
| `pgbouncer` | Пул соединений к PostgreSQL |
| `prometheus-0` | Сбор метрик |
| `grafana-0` | Дашборды |
| `loki-0` | Агрегация логов |
| `alloy` | Агент сбора логов |
