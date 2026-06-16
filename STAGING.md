# Staging Environment

Тестовый стенд проекта на одном сервере с Kubernetes.

## Доступ к сервисам

Все сервисы защищены Basic Auth.

| Сервис | URL | Описание |
|--------|-----|----------|
| Event Receiver | http://staging.dws.sidey383.ru | Приём входящих событий |
| Subscriptions API | http://subscriptions.staging.dws.sidey383.ru | Управление подписками |
| Kafka UI | http://kafka-ui.staging.dws.sidey383.ru | Просмотр топиков и сообщений Kafka |
| Grafana | http://grafana.staging.dws.sidey383.ru | Метрики и дашборды |
| Prometheus | http://prometheus.staging.dws.sidey383.ru | Raw метрики |

**Basic Auth:** `admin` / см. у тимлида

**Grafana:** логин `admin`, пароль — см. у тимлида

## Запуск тестов

Полная инструкция и список переменных — [tests/README.md](tests/README.md),
обзор — [docs/03-testing.md](docs/03-testing.md).

```powershell
# Функциональные кейсы 1-6 → functional.html (из корня репозитория)
$env:E2E_EVENT_RECEIVER_URL = "http://staging.dws.sidey383.ru"
$env:E2E_SUBSCRIPTIONS_URL  = "http://subscriptions.staging.dws.sidey383.ru"
$env:E2E_BASIC_AUTH_USER    = "admin"
$env:E2E_BASIC_AUTH_PASS    = "..."
$env:E2E_CALLBACK_HOST      = "<хост, достижимый из кластера>"
$env:E2E_CALLBACK_PORT      = "8089"
go test -json ./tests/suite/... | go run ./cmd/report-gen -out functional.html
```

> Колбэк-приёмник теста должен быть достижим со стороны delivery-service —
> задаётся через `E2E_CALLBACK_HOST:E2E_CALLBACK_PORT`.

## Сервисы в кластере

| Pod | Описание |
|-----|----------|
| `event-receiver` | HTTP API для приёма событий |
| `subscriptions-api` | CRUD API для управления подписками |
| `subscriptions-worker` | Обработка событий из Kafka, маршрутизация |
| `delivery-service` | Доставка webhook-ов на внешние URL |
| `delivery-dashboard` | UI статусов доставок |
| `kafka-dual-role-0` | Брокер сообщений (Strimzi) |
| `cassandra-dc1-default-sts-0` | База данных подписок (k8ssandra) |
| `delivery-postgres-0` | PostgreSQL — статусы доставок |
| `pgbouncer` | Пул соединений к PostgreSQL |
| `prometheus-0` | Сбор метрик |
| `grafana-0` | Визуализация метрик |
| `loki-0` | Агрегация логов |
| `alloy` | Агент сбора логов (Grafana Alloy) |
