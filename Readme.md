# Environment

Конфигурация окружения для webhook delivery engine.

Поддерживаются два способа запуска: **Kubernetes** (основной) и **Docker Compose** (быстрый старт).

---

## Архитектура

```
[Client] → event-receiver → Kafka → subscriptions-worker → Kafka → delivery-service → [Webhook]
                                            ↕
                                       Cassandra
                                    (subscriptions)
```

### Сервисы приложений

| Сервис | Описание | Порт |
|---|---|---|
| `event-receiver` | Принимает входящие события по HTTP | 8080 |
| `subscriptions-api` | REST API для управления подписками | 8082 |
| `subscriptions-worker` | Kafka consumer — маршрутизирует события по подпискам | 9091 (metrics) |
| `delivery-service` | Доставляет webhook-уведомления подписчикам | 9095 (metrics) |

### Инфраструктура

| Компонент | Описание |
|---|---|
| Kafka (KRaft) | Брокер сообщений |
| Cassandra | Хранилище подписок |
| Kafka UI | Веб-интерфейс для Kafka |
| Prometheus | Сбор метрик |
| Grafana | Дашборды (преднастроен дашборд Webhook Delivery Engine) |
| Loki + Grafana Alloy | Централизованные логи (только Kubernetes) |

---

## Kubernetes — установка с нуля

### Шаг 1. Docker Desktop

1. Скачать и установить [Docker Desktop](https://www.docker.com/products/docker-desktop/)
2. Открыть **Settings → Kubernetes → Enable Kubernetes** → Apply & Restart
3. Дождаться зелёного статуса Kubernetes в трее

Проверить, что `kubectl` видит кластер:

```bash
kubectl config current-context
# должно быть: docker-desktop
```

Если context другой (например, от minikube или kind), переключить:

```bash
kubectl config use-context docker-desktop
```

### Шаг 2. Nginx Ingress Controller

Ingress нужен, чтобы обращаться к сервисам по доменным именам (`kafka-ui.localhost` и т.д.) через порт 80.

```bash
kubectl apply -f https://raw.githubusercontent.com/kubernetes/ingress-nginx/controller-v1.11.1/deploy/static/provider/cloud/deploy.yaml
```

Подождать, пока контроллер запустится:

```bash
kubectl wait --namespace ingress-nginx \
  --for=condition=ready pod \
  --selector=app.kubernetes.io/component=controller \
  --timeout=120s
```

Проверить:

```bash
kubectl get pods -n ingress-nginx
# ingress-nginx-controller-xxx   1/1   Running
```

### Шаг 3. Токен GitHub для загрузки образов

Образы сервисов хранятся в GitHub Container Registry (ghcr.io) в приватных репозиториях.
Kubernetes нужен токен, чтобы скачать их.

**Создать токен в GitHub:**

1. Открыть [github.com → Settings](https://github.com/settings/profile) (иконка аватара → Settings)
2. Перейти в **Developer settings** (левый нижний пункт меню)
3. Выбрать **Personal access tokens → Tokens (classic)**
4. Нажать **Generate new token (classic)**
5. Дать название, например `k8s-ghcr-pull`
6. Выбрать срок действия (рекомендуется 90 дней или No expiration для стенда)
7. Отметить единственный нужный scope: **`read:packages`**
8. Нажать **Generate token** и скопировать токен — он показывается только один раз

**Создать секрет в Kubernetes:**

```bash
kubectl create namespace webhooks

kubectl create secret docker-registry ghcr-credentials \
  --namespace webhooks \
  --docker-server=ghcr.io \
  --docker-username=ВАШ_GITHUB_ЛОГИН \
  --docker-password=ВАШ_ТОКЕН
```

> Убедитесь, что ваш аккаунт имеет доступ к пакетам организации `dws-1-2026-green`.
> Если нет — попросите добавить вас в организацию или выдать доступ к пакетам.

### Шаг 4. Настройка hosts

Чтобы браузер резолвил `.localhost`-домены, добавить в файл hosts:

**Windows** — открыть от администратора: `C:\Windows\System32\drivers\etc\hosts`

**macOS / Linux** — `sudo nano /etc/hosts`

Добавить строки:

```
127.0.0.1 event-receiver.localhost
127.0.0.1 subscriptions.localhost
127.0.0.1 kafka-ui.localhost
127.0.0.1 prometheus.localhost
127.0.0.1 grafana.localhost
127.0.0.1 loki.localhost
```

### Шаг 5. Деплой

```bash
kubectl apply -k k8s/overlays/local
```

Дождаться запуска всех подов (первый раз может занять несколько минут — скачиваются образы):

```bash
kubectl get pods -n webhooks --watch
```

Все поды должны стать `Running`. Cassandra и зависящие от неё сервисы стартуют дольше остальных (~1-2 мин).

### Доступ к сервисам

| Сервис | URL | Логин |
|---|---|---|
| Event Receiver | http://event-receiver.localhost | — |
| Subscriptions API | http://subscriptions.localhost | — |
| Kafka UI | http://kafka-ui.localhost | — |
| Prometheus | http://prometheus.localhost | — |
| Grafana | http://grafana.localhost | admin / admin |
| Loki | http://loki.localhost | — |

### Прямой доступ к Kafka и Cassandra (только local overlay)

В local overlay для Kafka и Cassandra создаются отдельные `LoadBalancer`-сервисы. Docker Desktop автоматически выдаёт им `localhost` как external IP — можно подключаться напрямую с локальной машины без деплоя своего сервиса в кластер.

| Сервис    | Адрес с хоста    |
|-----------|------------------|
| Kafka     | `localhost:9092` |
| Cassandra | `localhost:9042` |

### Структура конфигурации

```
k8s/
├── base/
│   ├── namespace.yaml
│   ├── apps/             — event-receiver, subscriptions-api, subscriptions-worker, delivery-service
│   ├── messaging/        — kafka (+ Job автосоздания топиков), kafka-ui
│   ├── storage/          — cassandra (+ Job инициализации схемы)
│   └── observability/    — prometheus, grafana, loki, alloy
└── overlays/
    ├── local/            — ingress (.localhost домены), LoadBalancer для Kafka/Cassandra, host-listener для webhook-tool
    └── staging/          — ingress + переопределение секретов Grafana
```

---

## Docker Compose (упрощённый запуск)

Поднимает все сервисы без Kubernetes. Grafana показывает только метрики — логи через Loki не поддерживаются.

### Требования

- Docker Desktop (Kubernetes включать не нужно)

### Запуск

```bash
docker-compose up -d
```

### Доступ

| Сервис | URL |
|---|---|
| Event Receiver | http://localhost:8080 |
| Subscriptions API | http://localhost:8082 |
| Kafka UI | http://localhost:8081 |
| Prometheus | http://localhost:9090 |
| Grafana | http://localhost:3000 (admin / admin) |

### Kafka

| Параметр | Значение |
|---|---|
| Брокер с хоста | `localhost:9092` |
| Брокер внутри Docker | `kafka:9094` |

---

## Тестирование

Для ручного E2E-тестирования используется `webhook-tool` — интерактивная консольная утилита.
Подробнее: [APP_README.md](APP_README.md)

```bash
go build -o webhook-tool ./cmd/webhook-tool
./webhook-tool
```

---

## Связанные репозитории

| Репозиторий | Описание |
|---|---|
| [EventReceiver](https://github.com/dws-1-2026-green/EventReceiver) | HTTP-приёмник событий |
| [subscriptions](https://github.com/dws-1-2026-green/subscriptions) | API и worker для управления подписками |
| [delivery-service](https://github.com/dws-1-2026-green/delivery-service) | Сервис доставки webhook-уведомлений |
