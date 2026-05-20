# Обзор системы

> Следующий шаг: [02-deploy.md](02-deploy.md) — развернуть локально.

## Что это

Распределённая система доставки вебхуков. Принимает события от внешних систем, маршрутизирует их по подпискам и гарантированно доставляет HTTP-запросы получателям с логикой повтора.

## Поток данных

```
Внешняя система
    ↓  POST /sources/{source}/events
EventReceiver  (порт 8080)          ← HPA по CPU (1–5 реплик)
    ↓  Kafka: routing.requests
subscriptions-worker                ← KEDA по Kafka lag (1–5 реплик)
    ↓  Kafka: deliveries.to_send
delivery-service                    ← KEDA по Kafka lag (2–10 реплик)
    ↓  HTTP POST
Вебхук получателя
```

## Сервисы

| Сервис | Роль | Порт |
|--------|------|------|
| `event-receiver` | HTTP API — принимает события, публикует в Kafka | 8080 |
| `subscriptions-api` | REST API — управление подписками | 8082 |
| `subscriptions-worker` | Kafka consumer — сопоставляет события с подписками | 9091 (metrics) |
| `delivery-service` | Kafka consumer — доставляет HTTP-вебхуки с ретраями | 9095 (metrics) |

## Инфраструктура

| Компонент | Роль |
|-----------|------|
| Kafka (KRaft) | Шина сообщений между сервисами |
| Cassandra | Хранилище подписок |
| PostgreSQL | Хранилище статусов доставок (delivery-service) |
| Prometheus | Сбор метрик со всех сервисов |
| Grafana | Дашборды: overview, event-receiver, subscriptions, delivery |
| Loki + Alloy | Централизованные логи (только Kubernetes) |

## Kafka топики

| Топик | Продюсер | Консюмер |
|-------|----------|----------|
| `routing.requests` | event-receiver | subscriptions-worker |
| `deliveries.to_send` | subscriptions-worker | delivery-service |

## Ключевые архитектурные решения

- **At-least-once delivery** — Kafka commit только после успешной обработки
- **Trace ID** — UUID генерируется в event-receiver и тянется через все сервисы
- **Adapter pattern** — subscriptions: PostgreSQL/Cassandra взаимозаменяемы без изменения логики
- **Stateless сервисы** — горизонтальное масштабирование через Kafka consumer groups
- **Autoscaling** — event-receiver по CPU (HPA), kafka-консюмеры по lag (KEDA)

## Веб-интерфейсы (Kubernetes local)

| Интерфейс | URL |
|-----------|-----|
| Grafana | http://grafana.localhost |
| Prometheus | http://prometheus.localhost |
| Kafka UI | http://kafka-ui.localhost |
| Subscriptions API | http://subscriptions.localhost |

## Исходные репозитории сервисов

| Репозиторий | Описание |
|-------------|----------|
| [EventReceiver](https://github.com/dws-1-2026-green/EventReceiver) | HTTP-приёмник событий |
| [subscriptions](https://github.com/dws-1-2026-green/subscriptions) | API и worker подписок |
| [delivery-service](https://github.com/dws-1-2026-green/delivery-service) | Сервис доставки вебхуков |
