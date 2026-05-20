# Runbook: локальный запуск стека в Kubernetes

## Предварительные требования (один раз)

### Инструменты
- Docker Desktop с включённым Kubernetes (`Settings → Kubernetes → Enable Kubernetes`)
- kubectl (идёт в комплекте с Docker Desktop)
- Go 1.22+ (для запуска тестов)

### Nginx Ingress Controller
```bash
kubectl apply -f https://raw.githubusercontent.com/kubernetes/ingress-nginx/controller-v1.10.1/deploy/static/provider/cloud/deploy.yaml

kubectl wait --namespace ingress-nginx \
  --for=condition=ready pod \
  --selector=app.kubernetes.io/component=controller \
  --timeout=120s
```

### Файл hosts (права администратора)

Добавить в `C:\Windows\System32\drivers\etc\hosts`:
```
127.0.0.1 event-receiver.localhost
127.0.0.1 subscriptions.localhost
127.0.0.1 grafana.localhost
127.0.0.1 prometheus.localhost
127.0.0.1 kafka-ui.localhost
127.0.0.1 delivery-dashboard.localhost
```

---

## Шаг 1 — Основной стек

```bash
cd Environment

kubectl apply -k k8s/overlays/local
```

Дождаться готовности всех подов:
```bash
kubectl get pods -n webhooks -w
```

Все поды должны быть в статусе `Running` или `Completed` (Job-ы). Обычно 1–2 минуты.

### Проверка партиций Kafka

Если кластер поднимается не впервые и топики уже существуют с 1 партицией:
```bash
# Проверить
kubectl exec -n webhooks kafka-0 -- /opt/kafka/bin/kafka-topics.sh \
  --bootstrap-server localhost:9094 \
  --describe --topic deliveries.to_send

# Если PartitionCount=1 — увеличить:
kubectl exec -n webhooks kafka-0 -- /opt/kafka/bin/kafka-topics.sh \
  --bootstrap-server localhost:9094 \
  --alter --topic deliveries.to_send --partitions 2

kubectl exec -n webhooks kafka-0 -- /opt/kafka/bin/kafka-topics.sh \
  --bootstrap-server localhost:9094 \
  --alter --topic routing.requests --partitions 2
```

### Интерфейсы

| Сервис             | URL                                    |
|--------------------|----------------------------------------|
| Grafana            | http://grafana.localhost (admin/admin) |
| Kafka UI           | http://kafka-ui.localhost              |
| Prometheus         | http://prometheus.localhost            |
| Delivery Dashboard | http://delivery-dashboard.localhost    |

---

## Шаг 2 — Моки и load-тест

### 2.1 Собрать образ мока

Пересобирать при каждом изменении `cmd/mock-receiver/`:
```bash
# из папки Environment/
docker build -t mock-receiver:local ./cmd/mock-receiver
```

### 2.2 Задеплоить моки в кластер

```bash
kubectl apply -f tests/k8s/mocks.yaml

kubectl wait --for=condition=ready pod -l app=mock-reliable -n webhooks --timeout=60s
kubectl wait --for=condition=ready pod -l app=mock-flaky   -n webhooks --timeout=60s
kubectl wait --for=condition=ready pod -l app=mock-chaos   -n webhooks --timeout=60s
```

### 2.3 Зарегистрировать подписки

```bash
bash tests/k8s/mock-setup.sh
```

Скрипт дождётся готовности subscriptions-api и зарегистрирует 3 подписки для source `load-test`,
указывающие на k8s-сервисы моков внутри кластера.

### 2.4 Запустить load-тест

```bash
go test -v -run TestLoad_10RPS -timeout 15m ./tests/e2e/
```

---

## Шаг 3 — E2E тесты

```bash
go test -v ./tests/e2e/
```

Тесты используют `event-receiver.localhost` и `subscriptions.localhost` через Ingress.

---

## Остановка и сброс

```bash
# Остановить стек (данные в PVC сохранятся):
kubectl delete -k k8s/overlays/local

# Удалить моки:
kubectl delete -f tests/k8s/mocks.yaml

# Полный сброс включая данные:
kubectl delete namespace webhooks
```
