# DWS Green 2026 — Webhook Delivery Engine

Распределённая система доставки вебхуков: приём событий → маршрутизация по подпискам → гарантированная HTTP-доставка с ретраями.

## Документация

Вся документация в [`docs/`](docs/):

| | |
|--|--|
| [docs/00-index.md](docs/00-index.md) | **Начать здесь** — карта документации и структура проекта |
| [docs/01-overview.md](docs/01-overview.md) | Архитектура, сервисы, поток данных |
| [docs/02-deploy.md](docs/02-deploy.md) | Развёртывание в Kubernetes (локально и в облаке) |
| [docs/03-testing.md](docs/03-testing.md) | Нагрузочные и e2e тесты |
| [docs/04-load-plan.md](docs/04-load-plan.md) | Узкие места системы под нагрузкой |
| [docs/05-staging.md](docs/05-staging.md) | Staging стенд |
| [docs/06-tool.md](docs/06-tool.md) | Ручное тестирование через webhook-tool |

## Быстрый старт

**Docker Compose** (без Kubernetes):
```bash
cd docker
docker compose up -d
```

**Kubernetes** (полный стек):
```bash
kubectl apply -k k8s/overlays/local
```

Подробности — в [docs/02-deploy.md](docs/02-deploy.md).
