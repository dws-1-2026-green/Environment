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
│   ├── config/                    # ConfigMap-ы — конфигурация вынесена сюда
│   │   ├── shared.yaml            # KAFKA_BROKERS (общий для всех сервисов)
│   │   ├── subscriptions.yaml     # Cassandra config (api + worker)
│   │   └── delivery-service.yaml  # Конфиг delivery-service
│   ├── apps/                      # Deployments + Services
│   │   ├── event-receiver.yaml
│   │   ├── subscriptions-api.yaml
│   │   ├── subscriptions-worker.yaml
│   │   ├── delivery-service.yaml
│   │   ├── hpa-event-receiver.yaml      # HPA для event-receiver
│   │   └── keda-scaledobjects.yaml      # KEDA для kafka-консюмеров
│   ├── messaging/                 # Kafka + Kafka UI
│   ├── storage/                   # Cassandra + PostgreSQL
│   └── observability/             # Prometheus, Grafana, Loki, Alloy
└── overlays/
    ├── local/                     # Локальный кластер (Docker Desktop)
    │   ├── kustomization.yaml
    │   ├── ingress.yaml           # Ingress с *.localhost хостами
    │   ├── dev-ports.yaml         # LoadBalancer для внешнего доступа к Kafka/Cassandra
    │   └── host-listener.yaml
    └── staging/                   # Staging окружение
        ├── kustomization.yaml
        ├── ingress.yaml
        └── secrets.yaml
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
| `kubectl` | Управление кластером | Входит в Docker Desktop |
| `helm` | Установка KEDA, ingress, metrics-server | https://helm.sh/docs/intro/install/ |

```bash
kubectl version --client
helm version
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
  KAFKA_BROKERS: "kafka-0.kafka.webhooks.svc.cluster.local:9094"
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
    value: "kafka-0.kafka.webhooks.svc.cluster.local:9094"

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
helm repo add ingress-nginx https://kubernetes.github.io/ingress-nginx
helm repo update

helm install ingress-nginx ingress-nginx/ingress-nginx \
  --namespace ingress-nginx \
  --create-namespace

# Проверить — EXTERNAL-IP должен быть localhost
kubectl get svc -n ingress-nginx
```

### Шаг 3. Установить metrics-server

В Docker Desktop kubelet использует самоподписанный сертификат — нужен флаг `--kubelet-insecure-tls`:

```bash
helm repo add metrics-server https://kubernetes-sigs.github.io/metrics-server/
helm repo update

helm install metrics-server metrics-server/metrics-server \
  --namespace kube-system \
  --set args={--kubelet-insecure-tls}

kubectl get deployment metrics-server -n kube-system
```

### Шаг 4. Установить KEDA

```bash
helm repo add kedacore https://kedacore.github.io/charts
helm repo update

helm install keda kedacore/keda \
  --namespace keda \
  --create-namespace

kubectl get pods -n keda
# keda-operator-xxx                   1/1   Running
# keda-operator-metrics-apiserver-xxx 1/1   Running
```

### Шаг 5. Создать секреты

```bash
kubectl create namespace webhooks

# Доступ к GitHub Container Registry для скачивания образов
kubectl create secret docker-registry ghcr-credentials \
  --namespace webhooks \
  --docker-server=ghcr.io \
  --docker-username=ВАШ_GITHUB_USERNAME \
  --docker-password=ВАШ_GITHUB_TOKEN

# PostgreSQL для delivery-service
kubectl create secret generic delivery-postgres-credentials \
  --namespace webhooks \
  --from-literal=url="postgres://ds:ds-password@delivery-postgres-0.delivery-postgres.webhooks.svc.cluster.local:5432/delivery"
```

> Токен GitHub: Settings → Developer settings → Personal access tokens → scope `read:packages`.

### Шаг 6. Применить манифесты

```bash
# Из директории Environment/

# Предварительный просмотр (dry-run)
kubectl kustomize k8s/overlays/local

# Применить
kubectl apply -k k8s/overlays/local
```

### Шаг 7. Настроить локальный DNS

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

### Шаг 8. Дождаться готовности

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
# delivery-service       2     10    True    True
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
| subscriptions-worker | KEDA | Kafka lag / 100 | lag > 0 | 1→5 |
| delivery-service | KEDA | Kafka lag / 50 | lag > 0 | 2→10 |

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
| Ingress controller | Helm | Helm (то же) |
| metrics-server | Helm + `--kubelet-insecure-tls` | Уже есть (GKE, AKS) или helm без флага (EKS) |
| KEDA | Helm | Helm (то же) |
| Секреты | `kubectl create secret` | Рекомендуется: облачные хранилища секретов |
| StorageClass | `hostpath` | cloud-specific (gp2, pd-ssd и т.д.) |
| LoadBalancer | EXTERNAL-IP = localhost | EXTERNAL-IP = публичный IP |

### GKE

```bash
gcloud container clusters create dws-green \
  --num-nodes=3 --machine-type=e2-standard-4 --region=europe-central2

gcloud container clusters get-credentials dws-green --region=europe-central2

helm install keda kedacore/keda --namespace keda --create-namespace

kubectl create namespace webhooks
kubectl create secret docker-registry ghcr-credentials ...
kubectl create secret generic delivery-postgres-credentials ...

kubectl apply -k k8s/overlays/staging
```

### EKS

```bash
eksctl create cluster \
  --name dws-green --region eu-central-1 \
  --node-type t3.xlarge --nodes 3

# metrics-server в EKS нет по умолчанию
kubectl apply -f https://github.com/kubernetes-sigs/metrics-server/releases/latest/download/components.yaml

helm install keda kedacore/keda --namespace keda --create-namespace
kubectl apply -k k8s/overlays/staging
```

### AKS

```bash
az aks create --resource-group dws-green-rg --name dws-green \
  --node-count 3 --node-vm-size Standard_D4s_v3

az aks get-credentials --resource-group dws-green-rg --name dws-green

helm install keda kedacore/keda --namespace keda --create-namespace
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

# Проверить партиции Kafka
kubectl exec -n webhooks kafka-0 -- /opt/kafka/bin/kafka-topics.sh \
  --bootstrap-server localhost:9094 \
  --describe --topic deliveries.to_send

# Увеличить партиции (только вверх, обратно нельзя)
kubectl exec -n webhooks kafka-0 -- /opt/kafka/bin/kafka-topics.sh \
  --bootstrap-server localhost:9094 \
  --alter --topic deliveries.to_send --partitions 4
```
