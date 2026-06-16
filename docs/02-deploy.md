# Развёртывание в Kubernetes

> Обзор системы: [01-overview.md](01-overview.md).
> Тестирование после деплоя: [03-testing.md](03-testing.md).

## Содержание

1. [Структура манифестов](#1-структура-манифестов)
2. [Необходимые инструменты](#2-необходимые-инструменты)
3. [Как устроена конфигурация](#3-как-устроена-конфигурация)
4. [Локальное развёртывание (Docker Desktop)](#4-локальное-развёртывание-docker-desktop)
5. [Проверка работоспособности](#5-проверка-работоспособности)
6. [Autoscaling: как работает и как проверить](#6-autoscaling-как-работает-и-как-проверить)
7. [Моки для load-тестирования](#7-моки-для-load-тестирования)
8. [Облачное развёртывание](#8-облачное-развёртывание)
9. [Разворачивание в новом окружении](#9-разворачивание-в-новом-окружении)

---

## 1. Структура манифестов

```
k8s/
├── base/                          # Общая база для всех окружений
│   ├── kustomization.yaml
│   ├── namespace.yaml             # Namespace: webhooks
│   ├── config/                    # ConfigMap-ы с конфигурацией сервисов
│   │   ├── shared.yaml            # KAFKA_BROKERS (общий для всех)
│   │   ├── event-receiver.yaml
│   │   ├── subscriptions.yaml     # Cassandra config (api + worker)
│   │   └── delivery-service.yaml
│   ├── apps/                      # Service + Deployment + autoscaler на сервис
│   │   ├── event-receiver.yaml         # + HPA по CPU
│   │   ├── subscriptions-api.yaml      # + HPA по CPU
│   │   ├── subscriptions-worker.yaml   # + KEDA ScaledObject (Kafka lag)
│   │   └── delivery-service.yaml        # + KEDA ScaledObject (Kafka lag)
│   ├── messaging/                 # Kafka (Strimzi) + Kafka UI
│   ├── storage/                   # Cassandra (k8ssandra) + PostgreSQL + PgBouncer
│   ├── observability/             # Prometheus, Grafana, Loki, Alloy, delivery-dashboard
│   ├── dashboards/                # JSON-дашборды Grafana
│   └── platform/                  # Операторы (отдельный apply, см. шаг 3)
│       ├── cluster-infra/         # cert-manager, KEDA, metrics-server
│       └── k8ssandra/             # k8ssandra + cass-operator + Strimzi
├── ingress-nginx/                 # Ingress controller (отдельный apply)
│   ├── base/
│   └── overlays/                  # demo / staging
└── overlays/
    ├── local/                     # Docker Desktop (*.localhost)
    │   ├── kustomization.yaml
    │   ├── ingress.yaml           # Ingress с *.localhost хостами
    │   ├── dev-ports.yaml         # Внешний доступ к Kafka/Cassandra
    │   └── host-listener.yaml
    ├── demo/                      # demo.dws.sidey383.ru
    └── staging/                   # staging.dws.sidey383.ru
        ├── kustomization.yaml
        ├── ingress.yaml
        ├── secrets.yaml
        └── cassandra-schema-patch.yaml
```

### Принцип base + overlays (Kustomize)

`base/` — полное описание системы с дефолтными значениями.
`overlays/` — только **отличия**: другие адреса, другие секреты, дополнительные ресурсы.

При добавлении нового окружения (prod) — создаёшь папку в `overlays/`, пишешь только то, что отличается. Базовые манифесты не трогаешь.

---

## 2. Необходимые инструменты

| Инструмент | Назначение | Установка |
|-----------|-----------|-----------|
| `Docker Desktop` | Локальный кластер (встроенный k8s) | https://www.docker.com/products/docker-desktop/ |
| `kubectl` | Управление кластером + сборка kustomize (`-k`) | Входит в Docker Desktop |

Операторы (ingress-nginx, cert-manager, KEDA, metrics-server, Strimzi, k8ssandra)
ставятся напрямую через `kubectl apply -k` — helm не требуется.

```bash
kubectl version --client
```

---

## 3. Как устроена конфигурация

### ConfigMap — для несекретных данных

```yaml
# k8s/base/config/shared.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: shared-config
data:
  KAFKA_BROKERS: "kafka-kafka-bootstrap.webhooks.svc.cluster.local:9092"
```

### Secret — для паролей и DSN

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: delivery-postgres-credentials
stringData:
  url: "postgres://ds:password@host:5432/delivery"
```

### Как Deployment использует ConfigMap

```yaml
# Было (плохо — конфиг внутри деплоймента):
env:
  - name: KAFKA_BROKERS
    value: "kafka-kafka-bootstrap.webhooks.svc.cluster.local:9092"

# Стало (хорошо — конфиг снаружи):
envFrom:
  - configMapRef:
      name: shared-config       # все ключи становятся переменными окружения
env:
  - name: DATABASE_URL
    valueFrom:
      secretKeyRef:
        name: delivery-postgres-credentials
        key: url
```

### Переопределение конфига в overlay

Если в staging нужен другой адрес Cassandra — в `k8s/overlays/staging/kustomization.yaml`:

```yaml
configMapGenerator:
  - name: subscriptions-config
    behavior: merge
    literals:
      - CASSANDRA_HOSTS=cassandra.staging.internal
      - CASSANDRA_CONSISTENCY=QUORUM
```

Базовый ConfigMap остаётся нетронутым.

---

## 4. Локальное развёртывание (Docker Desktop)

### Шаг 1. Включить Kubernetes в Docker Desktop

**Settings → Kubernetes → Enable Kubernetes → Apply & Restart**

> В Settings → Resources выдели минимум **6 GB RAM** и **4 CPU** (Kafka и Cassandra требовательны).

Проверить:
```bash
kubectl config current-context
# docker-desktop

kubectl get nodes
# NAME             STATUS   ROLES           AGE
# docker-desktop   Ready    control-plane   1m
```

Если контекст другой:
```bash
kubectl config use-context docker-desktop
```

### Шаг 2. Установить nginx ingress controller

```bash
kubectl apply -k k8s/ingress-nginx/base

# Проверить — EXTERNAL-IP должен быть localhost
kubectl get svc -n ingress-nginx
```

### Шаг 3. Установить операторы платформы

Один шаг ставит cert-manager, KEDA, metrics-server, Strimzi и k8ssandra/cass-operator:

```bash
kubectl apply -k k8s/base/platform --server-side
```

> `--server-side` обязателен — CRD операторов слишком большие для client-side apply.

В Docker Desktop kubelet использует самоподписанный сертификат — добавь metrics-server
флаг `--kubelet-insecure-tls`, иначе HPA не получит метрики CPU:

```bash
kubectl patch deployment metrics-server -n kube-system --type=json \
  -p '[{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--kubelet-insecure-tls"}]'
```

Дождись готовности операторов перед применением манифестов (CR Kafka/Cassandra
требуют установленных CRD и запущенных операторов):

```bash
kubectl wait --for=condition=Available --timeout=300s \
  deployment -n cert-manager --all
kubectl get pods -n keda
kubectl get pods -n webhooks -l app.kubernetes.io/name=strimzi-cluster-operator
```

### Шаг 4. Создать секреты

```bash
kubectl create namespace webhooks

# Доступ к GitHub Container Registry для скачивания образов
kubectl create secret docker-registry ghcr-credentials \
  --namespace webhooks \
  --docker-server=ghcr.io \
  --docker-username=ВАШ_GITHUB_USERNAME \
  --docker-password=ВАШ_GITHUB_TOKEN
```

> Токен GitHub: Settings → Developer settings → Personal access tokens → scope `read:packages`.
>
> Секрет `delivery-postgres-credentials` создаётся автоматически из
> `k8s/base/storage/postgres.yaml` — вручную его создавать не нужно.

### Шаг 5. Применить манифесты

```bash
# Из директории Environment/

# Предварительный просмотр (dry-run)
kubectl kustomize k8s/overlays/local

# Применить
kubectl apply -k k8s/overlays/local
```

### Шаг 6. Настроить локальный DNS

Открой с правами администратора `C:\Windows\System32\drivers\etc\hosts` и добавь:

```
127.0.0.1  event-receiver.localhost
127.0.0.1  subscriptions.localhost
127.0.0.1  kafka-ui.localhost
127.0.0.1  prometheus.localhost
127.0.0.1  grafana.localhost
127.0.0.1  loki.localhost
127.0.0.1  delivery-dashboard.localhost
```

### Шаг 7. Дождаться готовности

Cassandra и Kafka стартуют 1–3 минуты. Сервисы ждут их через `initContainers`.

```bash
kubectl get pods -n webhooks -w
# Init:0/1  — initContainer ждёт Cassandra/PostgreSQL (нормально, ждём)
# Running   — сервис работает
```

---

## 5. Проверка работоспособности

```bash
# Поды запущены?
kubectl get pods -n webhooks

# ConfigMap-ы созданы?
kubectl get configmaps -n webhooks

# HPA работает?
kubectl get hpa -n webhooks
# event-receiver   Deployment/event-receiver   cpu: 5%/70%   1   5   1

# KEDA ScaledObjects активны?
kubectl get scaledobject -n webhooks
# NAME                   MIN   MAX   READY   ACTIVE
# delivery-service       1     10    True    True
# subscriptions-worker   1     5     True    True
```

Тестовое событие:
```bash
curl -X POST http://event-receiver.localhost/sources/crm-1/events \
  -H "Content-Type: application/json" \
  -d '{"id":"evt-1","type":"order.created","created_at":"2026-05-21T10:00:00Z","data":{"order_id":"123"}}'
```

Веб-интерфейсы:

| Интерфейс | URL |
|-----------|-----|
| Grafana | http://grafana.localhost (admin/admin) |
| Prometheus | http://prometheus.localhost |
| Kafka UI | http://kafka-ui.localhost |
| Subscriptions API | http://subscriptions.localhost |

---

## 6. Autoscaling: как работает и как проверить

| Сервис | Механизм | Метрика | Порог | Min→Max |
|--------|----------|---------|-------|---------|
| event-receiver | HPA | CPU utilization | 70% | 1→5 |
| subscriptions-api | HPA | CPU utilization | 70% | 1→5 |
| subscriptions-worker | KEDA | Kafka lag / 100 | lag > 0 | 1→5 |
| delivery-service | KEDA | Kafka lag / 50 | lag > 0 | 1→10 |

**Почему lag, а не CPU для Kafka-консюмеров:** delivery-service ждёт HTTP-ответов от вебхуков — CPU низкий, но очередь растёт. Lag честнее отражает реальную нагрузку.

**Расчёт реплик KEDA:** `ceil(lag / lagThreshold)`. При lag=300 для delivery-service: `ceil(300/50) = 6 реплик`.

Проверить:
```bash
kubectl describe hpa event-receiver -n webhooks
kubectl describe scaledobject delivery-service -n webhooks
kubectl get events -n webhooks --field-selector reason=KEDAScaleTargetActivated
```

---

## 7. Моки для load-тестирования

Моки разворачиваются в namespace `webhooks-test` — изолированно от основного стека. Delivery-service достучивается до них по внутреннему DNS:

```
http://mock-reliable.webhooks-test.svc.cluster.local:8080
```

Подробный сценарий тестирования — в [03-testing.md](03-testing.md).

---

## 8. Облачное развёртывание

### Отличия от локального

| Аспект | Docker Desktop | Облако (GKE/EKS/AKS) |
|--------|---------------|----------------------|
| Ingress IP | `127.0.0.1` | Выдаётся облаком автоматически |
| Ingress controller | `k8s/ingress-nginx/base` | `k8s/ingress-nginx/overlays/{demo,staging}` |
| Операторы платформы | `k8s/base/platform` | `k8s/base/platform` (то же) |
| metrics-server | + `--kubelet-insecure-tls` | без флага (входит в platform) |
| Секреты | `kubectl create secret` | Рекомендуется: облачные хранилища секретов |
| StorageClass | `hostpath` | cloud-specific (gp2, pd-ssd, csi-ceph-hdd) |
| LoadBalancer | EXTERNAL-IP = localhost | EXTERNAL-IP = публичный IP |

Порядок применения везде одинаков: **ingress-nginx → platform (ждём операторов) → overlay**.
Storage class для облака задаётся в overlay (см. `overlays/demo`, `overlays/staging`).

### GKE

```bash
gcloud container clusters create dws-green \
  --num-nodes=3 --machine-type=e2-standard-4 --region=europe-central2

gcloud container clusters get-credentials dws-green --region=europe-central2

kubectl apply -k k8s/ingress-nginx/overlays/staging
kubectl apply -k k8s/base/platform --server-side

kubectl create namespace webhooks
kubectl create secret docker-registry ghcr-credentials ...

kubectl apply -k k8s/overlays/staging
```

### EKS

```bash
eksctl create cluster \
  --name dws-green --region eu-central-1 \
  --node-type t3.xlarge --nodes 3

kubectl apply -k k8s/ingress-nginx/overlays/staging
kubectl apply -k k8s/base/platform --server-side   # включает metrics-server

kubectl create namespace webhooks
kubectl create secret docker-registry ghcr-credentials ...
kubectl apply -k k8s/overlays/staging
```

### AKS

```bash
az aks create --resource-group dws-green-rg --name dws-green \
  --node-count 3 --node-vm-size Standard_D4s_v3

az aks get-credentials --resource-group dws-green-rg --name dws-green

kubectl apply -k k8s/ingress-nginx/overlays/staging
kubectl apply -k k8s/base/platform --server-side

kubectl create namespace webhooks
kubectl create secret docker-registry ghcr-credentials ...
kubectl apply -k k8s/overlays/staging
```

---

## 9. Разворачивание в новом окружении

```bash
mkdir -p k8s/overlays/prod  # из директории Environment/
```

```yaml
# k8s/overlays/prod/kustomization.yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

resources:
  - ../../base
  - ingress.yaml

configMapGenerator:
  - name: shared-config
    behavior: merge
    literals:
      - KAFKA_BROKERS=kafka-prod.internal:9094

  - name: subscriptions-config
    behavior: merge
    literals:
      - CASSANDRA_HOSTS=cassandra-prod.internal
      - CASSANDRA_CONSISTENCY=QUORUM

  - name: delivery-service-config
    behavior: merge
    literals:
      - CONSUMER_WORKERS=100

secretGenerator:
  - name: grafana-credentials
    namespace: webhooks
    literals:
      - admin-user=admin
      - admin-password=STRONG_PROD_PASSWORD
    behavior: replace
    options:
      disableNameSuffixHash: true
```

```bash
kubectl create namespace webhooks
kubectl create secret docker-registry ghcr-credentials ...
kubectl create secret generic delivery-postgres-credentials \
  --from-literal=url="postgres://ds:PROD_PASSWORD@prod-postgres:5432/delivery"

kubectl apply -k k8s/overlays/prod

# Предварительный просмотр
kubectl kustomize k8s/overlays/prod | less
```

---

## Шпаргалка

```bash
# Применить
kubectl apply -k k8s/overlays/local

# Все ресурсы namespace
kubectl get all -n webhooks

# Детали пода (события, ошибки)
kubectl describe pod <pod-name> -n webhooks

# Зайти внутрь пода
kubectl exec -it <pod-name> -n webhooks -- sh

# Переменные окружения пода (проверить ConfigMap)
kubectl exec -it <pod-name> -n webhooks -- env | sort

# Удалить всё и начать заново
kubectl delete -k k8s/overlays/local
kubectl apply -k k8s/overlays/local

# Обновить образ без изменения манифеста
kubectl set image deployment/event-receiver \
  event-receiver=ghcr.io/dws-1-2026-green/eventreceiver:new-tag \
  -n webhooks

# Проверить партиции Kafka (под Strimzi: kafka-dual-role-0)
kubectl exec -n webhooks kafka-dual-role-0 -- /opt/kafka/bin/kafka-topics.sh \
  --bootstrap-server kafka-kafka-bootstrap:9092 \
  --describe --topic deliveries.to_send

# Партиции лучше менять через KafkaTopic CR (Strimzi topic operator):
# отредактируй spec.partitions в k8s/base/messaging/kafka.yaml и применить заново.
```
