# Тестирование

> Предварительно: стек должен быть поднят — см. [02-deploy.md](02-deploy.md).
> Анализ узких мест под нагрузкой: [04-load-plan.md](04-load-plan.md).
> Ручное тестирование через утилиту: [06-tool.md](06-tool.md).

Два режима тестирования:

| Режим | Когда использовать |
|-------|-------------------|
| **Kubernetes** | Полное e2e + нагрузочные тесты с реальным autoscaling |
| **Docker Compose** | Быстрые нагрузочные тесты без k8s |

---

## Режим 1 — Kubernetes

### Шаг 1. Убедиться что основной стек работает

```bash
kubectl get pods -n webhooks
# Все поды в статусе Running
```

Если не поднят — см. [02-deploy.md](02-deploy.md).

### Шаг 2. Собрать образ мока

Мок-ресивер симулирует реальный вебхук-получатель с настраиваемым хаосом.

```bash
# Из директории Environment/
docker build -t mock-receiver:local ./cmd/mock-receiver
```

Пересобирать при изменениях в `cmd/mock-receiver/`. `imagePullPolicy: Never` в манифесте означает: k8s использует только локально собранный образ.

### Шаг 3. Задеплоить моки

Моки разворачиваются в отдельном namespace `webhooks-test` — изолированно от основного стека. Delivery-service достучивается до них по внутреннему DNS: `http://mock-reliable.webhooks-test.svc.cluster.local:8080`.

```bash
kubectl apply -f tests/k8s/mocks.yaml

# Дождаться готовности
kubectl wait --for=condition=ready pod -l app=mock-reliable -n webhooks-test --timeout=60s
kubectl wait --for=condition=ready pod -l app=mock-flaky   -n webhooks-test --timeout=60s
kubectl wait --for=condition=ready pod -l app=mock-chaos   -n webhooks-test --timeout=60s
```

Три мока с разными профилями хаоса:

| Мок | client_error% | error% | reset% | slow% | Назначение |
|-----|:---:|:---:|:---:|:---:|-----------|
| `mock-reliable` | 2 | 3 | 2 | 10 | Почти всегда отвечает 200 |
| `mock-flaky` | 5 | 15 | 10 | 20 | Умеренные ошибки |
| `mock-chaos` | 10 | 30 | 20 | 15 | Максимальный хаос |

- `client-error` → 400, delivery-service не ретраит
- `error` → 503, запускает exponential-backoff retry
- `reset` → TCP RST, аналог таймаута, запускает retry

### Шаг 4. Зарегистрировать подписки

```bash
bash tests/k8s/mock-setup.sh
```

Скрипт дождётся готовности subscriptions-api и зарегистрирует 3 подписки для source `load-test`, указывающие на k8s-сервисы моков в namespace `webhooks-test`.

Проверить что подписки созданы:
```bash
curl http://subscriptions.localhost/api/v1/subscriptions | grep load-test
```

### Шаг 5. Запустить тесты

```bash
# 10 RPS в течение 10 минут
go test -v -run TestLoad_10RPS  -timeout 15m ./tests/e2e/

# 50 RPS
go test -v -run TestLoad_50RPS  -timeout 15m ./tests/e2e/

# 100 RPS
go test -v -run TestLoad_100RPS -timeout 15m ./tests/e2e/
```

Каждые 30 секунд в лог выводится: `sent / ok / err / actual rps`.

### Шаг 6. Наблюдение во время теста

Открой в браузере:

| Что смотреть | URL | На что обращать внимание |
|-------------|-----|--------------------------|
| Overview | http://grafana.localhost → Overview | Kafka Lag — если растёт, консюмеры не справляются |
| Event Receiver | http://grafana.localhost → Event Receiver | HTTP latency p99, ошибки публикации в Kafka |
| Delivery | http://grafana.localhost → Delivery Service | Pending deliveries, attempt duration |
| Kafka UI | http://kafka-ui.localhost | Consumer group lag в реальном времени |

Логи моков:
```bash
kubectl logs -n webhooks-test deployment/mock-reliable -f
kubectl logs -n webhooks-test deployment/mock-chaos    -f
```

Autoscaling в действии:
```bash
# Смотреть как KEDA добавляет реплики при росте lag
kubectl get scaledobject -n webhooks -w

# HPA для event-receiver
kubectl get hpa -n webhooks -w
```

### Завершение и очистка

```bash
# Удалить только моки (основной стек не трогать)
kubectl delete -f tests/k8s/mocks.yaml
kubectl delete namespace webhooks-test

# Удалить подписки load-test (чтобы не засорять)
# Через Subscriptions API или напрямую в Cassandra
```

---

## Режим 2 — Docker Compose

Быстрый способ без k8s. Моки запускаются как Docker-контейнеры, подписки регистрируются автоматически.

### Запуск

```bash
cd docker
docker compose -f docker-compose.yml -f docker-compose.stress.yml up -d --build
```

`docker-compose.stress.yml` добавляет три мока и сервис `mock-setup`, который автоматически ждёт subscriptions-api и регистрирует подписки.

### Дождаться регистрации подписок

```bash
docker logs mock-setup
# Ожидаемый вывод:
# registered → mock-reliable
# registered → mock-flaky
# registered → mock-chaos
# mock-setup done
```

Cassandra стартует ~30–60 секунд, `mock-setup` ждёт автоматически.

### Запустить тест

```bash
# Из директории Environment/ (не docker/)
go test -v -run TestLoad_10RPS  -timeout 15m ./tests/e2e/
go test -v -run TestLoad_50RPS  -timeout 15m ./tests/e2e/
go test -v -run TestLoad_100RPS -timeout 15m ./tests/e2e/
```

### Мониторинг

| Интерфейс | URL |
|-----------|-----|
| Grafana | http://localhost:3000 (admin / admin) |
| Kafka UI | http://localhost:8081 |
| Prometheus | http://localhost:9090 |

Логи моков:
```bash
docker logs -f mock-reliable
docker logs -f mock-chaos
```

### Остановка

```bash
cd docker

# Остановить (данные в volumes сохранятся):
docker compose -f docker-compose.yml -f docker-compose.stress.yml down

# Полный сброс включая данные:
docker compose -f docker-compose.yml -f docker-compose.stress.yml down -v
```

> **Повторный запуск**: подписки хранятся в Cassandra. При рестарте без `-v` `mock-setup` создаст дубли подписок — для чистого старта используй `-v`.

---

## E2E тесты

Проверяют базовую работоспособность — отправку события и наличие подписок:

```bash
# Против локального k8s
go test -v ./tests/e2e/ -run TestE2E

# Против staging
go test ./tests/e2e/ -v -run "TestStaging" -timeout 120s
```

Для staging — переменные окружения описаны в [05-staging.md](05-staging.md).

---

## k6 нагрузочные тесты

Альтернативный инструмент для нагрузки через `tests/load/webhook_load_test.js`:

```bash
# Windows (из директории Environment/)
.\run-load-tests.bat

# Против конкретного адреса
.\run-load-tests.bat http://event-receiver.localhost
```

Скрипт автоматически использует локально установленный k6 или Docker-образ.
