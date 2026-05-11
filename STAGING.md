# Staging Environment

Тестовый стенд проекта на одном сервере с Kubernetes.

## Доступ к сервисам

Все сервисы защищены Basic Auth.

| Сервис | URL | Описание |
|--------|-----|----------|
| Event Receiver | http://staging.dws.sidey383.ru | Приём входящих событий |
| Subscriptions API | http://subscriptions.dws.sidey383.ru | Управление подписками |
| Kafka UI | http://kafka-ui.dws.sidey383.ru | Просмотр топиков и сообщений Kafka |
| Grafana | http://grafana.dws.sidey383.ru | Метрики и дашборды |
| Prometheus | http://prometheus.dws.sidey383.ru | Raw метрики |

**Basic Auth:** `admin` / см. у тимлида

**Grafana:** логин `admin`, пароль — см. у тимлида

## Запуск тестов

```powershell
# Staging тесты (из корня репозитория)
go test ./tests/e2e/ -v -run "TestStaging" -timeout 120s
```

Переменные окружения (опционально, есть дефолты):
```powershell
$env:E2E_EVENT_RECEIVER_URL   = "http://staging.dws.sidey383.ru"
$env:E2E_SUBSCRIPTIONS_URL    = "http://subscriptions.dws.sidey383.ru"
$env:E2E_BASIC_AUTH_USER      = "admin"
$env:E2E_BASIC_AUTH_PASS      = "..."
```

## Сервисы в кластере

| Pod | Описание |
|-----|----------|
| `event-receiver` | HTTP API для приёма событий |
| `subscriptions-api` | CRUD API для управления подписками |
| `subscriptions-worker` | Обработка событий из Kafka, маршрутизация |
| `delivery-service` | Доставка webhook-ов на внешние URL |
| `kafka-0` | Брокер сообщений |
| `cassandra-0` | База данных подписок |
| `prometheus-0` | Сбор метрик |
| `grafana-0` | Визуализация метрик |
| `loki-0` | Агрегация логов |
| `alloy` | Агент сбора логов (Grafana Alloy) |
