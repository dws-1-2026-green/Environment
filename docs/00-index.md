# Документация — DWS Green 2026 Webhook Engine

## Что читать и зачем

| Файл | Для кого | Когда читать |
|------|----------|--------------|
| **[01-overview.md](01-overview.md)** | Все | Первый заход в проект — архитектура, сервисы, поток данных |
| **[02-deploy.md](02-deploy.md)** | DevOps, разработчики | Поднять стек в Kubernetes локально или в облаке |
| **[03-testing.md](03-testing.md)** | QA, разработчики | Запустить моки, нагрузочные и e2e тесты |
| **[04-load-plan.md](04-load-plan.md)** | Разработчики | Понять узкие места системы, уровни нагрузки |
| **[05-staging.md](05-staging.md)** | Все | Доступ к staging-стенду |
| **[06-tool.md](06-tool.md)** | Разработчики | Ручное e2e тестирование через webhook-tool |

## Рекомендуемый порядок для нового участника

```
01-overview.md          ← понять что за система
       ↓
02-deploy.md            ← поднять локально
       ↓
06-tool.md              ← убедиться что всё работает вручную
       ↓
03-testing.md           ← запустить нагрузочные тесты
       ↓
04-load-plan.md         ← изучить как система ведёт себя под нагрузкой
```

## Структура репозитория

```
Environment/
├── docs/               ← вся документация (вы здесь)
├── docker/             ← Docker Compose стек (быстрый локальный запуск)
│   ├── docker-compose.yml
│   ├── docker-compose.stress.yml
│   ├── mock-setup.sh
│   ├── cassandra/
│   ├── grafana/
│   └── prometheus/
├── k8s/                ← Kubernetes стек (основной)
│   ├── base/           ← манифесты сервисов + platform/ (операторы)
│   ├── ingress-nginx/  ← ingress controller (отдельный apply)
│   └── overlays/       ← local / demo / staging окружения
├── cmd/                ← исходники утилит
│   ├── mock-receiver/  ← мок вебхук-получателя (для k6-нагрузки)
│   ├── report-gen/     ← генератор HTML-отчёта из go test -json
│   └── webhook-tool/   ← консольный инструмент ручного тестирования
└── tests/
    ├── suite/          ← функциональные + closed-loop тесты (Go, кейсы 1-6)
    ├── load/           ← k6 скрипты (опциональная нагрузка)
    ├── docker/         ← Dockerfile + entrypoint образа тестов
    ├── k8s/            ← моки, Job для in-cluster прогона
    └── README.md       ← полная инструкция и env-переменные
```

## Два способа запуска

**Docker Compose** — быстрый старт без k8s, достаточно для разработки:
```bash
cd docker
docker compose up -d
```

**Kubernetes** — полный стек с autoscaling, логами, метриками:
```bash
kubectl apply -k k8s/overlays/local
```

Подробности в [02-deploy.md](02-deploy.md).
