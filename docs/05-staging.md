# Staging окружение

> Связанные документы: [02-deploy.md](02-deploy.md) — развернуть своё окружение.

Тестовый стенд на одном сервере с Kubernetes.

## Доступ к сервисам

Все сервисы защищены Basic Auth.

| Сервис | URL |
|--------|-----|
| Event Receiver | http://staging.dws.sidey383.ru |
| Subscriptions API | http://subscriptions.dws.sidey383.ru |
| Kafka UI | http://kafka-ui.dws.sidey383.ru |
| Grafana | http://grafana.dws.sidey383.ru |
| Prometheus | http://prometheus.dws.sidey383.ru |

**Basic Auth и пароль Grafana:** см. у тимлида.

## Запуск тестов против staging

```powershell
go test ./tests/e2e/ -v -run "TestStaging" -timeout 120s
```

Переменные окружения (есть дефолты, совпадающие со staging URL):
```powershell
$env:E2E_EVENT_RECEIVER_URL = "http://staging.dws.sidey383.ru"
$env:E2E_SUBSCRIPTIONS_URL  = "http://subscriptions.dws.sidey383.ru"
$env:E2E_BASIC_AUTH_USER    = "admin"
$env:E2E_BASIC_AUTH_PASS    = "..."
```

## Что запущено в кластере

| Pod | Описание |
|-----|----------|
| `event-receiver` | HTTP API приёма событий |
| `subscriptions-api` | CRUD API подписок |
| `subscriptions-worker` | Kafka consumer, маршрутизация |
| `delivery-service` | Доставка вебхуков |
| `kafka-0` | Брокер сообщений |
| `cassandra-0` | БД подписок |
| `prometheus-0` | Сбор метрик |
| `grafana-0` | Дашборды |
| `loki-0` | Агрегация логов |
| `alloy` | Агент сбора логов |
