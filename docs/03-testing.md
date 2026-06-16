# Тестирование

> Предварительно: стек должен быть поднят — см. [02-deploy.md](02-deploy.md).
> Полный список переменных окружения и команд — [tests/README.md](../tests/README.md).
> Анализ узких мест под нагрузкой: [04-load-plan.md](04-load-plan.md).
> Ручное тестирование через утилиту: [06-tool.md](06-tool.md).

Тесты — **ручные, но автоматизированные**: запускаются по команде тестировщиком,
настраиваются через переменные окружения, генерируют HTML-отчёты.

| Слой | Кейсы | Инструмент | Отчёт |
|------|-------|-----------|-------|
| Функциональные | 1 доставка · 2 ретраи · 3 частичный приём · 4 fan-out · 5 смена подписки (PUT) · 6 удаление подписки (DELETE) | Go (`tests/suite`) | `functional.html` |
| Нагрузка/стресс (closed-loop) | отправка + поглощение всех доставок, latency/throughput/pending | Go (`tests/suite`) | `load.html` / `stress.html` |
| Нагрузка (k6, опционально) | детальные HTTP-перцентили приёма | k6 (`tests/load`) | `k6-report.html` |

Подробности по каждому кейсу и всем env-переменным — в [tests/README.md](../tests/README.md).

---

## Способ 1 — Docker-образ (рекомендуется)

Один образ со всеми тестами, переносим между машинами.

```bash
# Собрать (из корня репозитория)
docker build -f tests/docker/Dockerfile -t webhook-tests:local .

# Функциональные кейсы 1-6 → out/functional.html
docker run --rm -v "$PWD/out:/reports" -p 8089:8089 \
  -e E2E_EVENT_RECEIVER_URL=http://staging.dws.sidey383.ru \
  -e E2E_SUBSCRIPTIONS_URL=http://subscriptions.staging.dws.sidey383.ru \
  -e E2E_BASIC_AUTH_USER=admin -e E2E_BASIC_AUTH_PASS=*** \
  -e E2E_CALLBACK_HOST=<хост-достижимый-из-кластера> -e E2E_CALLBACK_PORT=8089 \
  webhook-tests:local functional

# Closed-loop нагрузка → out/load.html
docker run --rm -v "$PWD/out:/reports" -p 8089:8089 \
  -e E2E_EVENT_RECEIVER_URL=... -e E2E_SUBSCRIPTIONS_URL=... \
  -e E2E_BASIC_AUTH_USER=admin -e E2E_BASIC_AUTH_PASS=*** \
  -e E2E_CALLBACK_HOST=<...> -e E2E_LOAD_RPS=50 -e E2E_LOAD_EVENTS=500 \
  webhook-tests:local load
```

> **Windows + Git Bash:** для проброса `out/` используйте абсолютный путь и
> `MSYS_NO_PATHCONV=1`, иначе репорт останется внутри контейнера:
> `MSYS_NO_PATHCONV=1 docker run ... -v "C:/.../out:/reports" ...`
>
> **Колбэк:** функциональные и closed-loop тесты поднимают приёмник вебхуков
> внутри контейнера; `E2E_CALLBACK_HOST:E2E_CALLBACK_PORT` должен быть достижим
> со стороны delivery-service (для Docker Desktop на Windows может потребоваться
> правило firewall на входящий порт). k6-путь этого не требует (только исходящий).

Команды образа: `functional` · `load` · `stress` · `all` · `k6-load` · `k6-stress`.

---

## Способ 2 — Нативно (без Docker)

```bash
# Функциональные → HTML (report-gen парсит go test -json)
go test -json ./tests/suite/... | go run ./cmd/report-gen -out functional.html

# Один кейс
go test -v ./tests/suite/ -run TestCase2_RetryOn5xx

# Closed-loop нагрузка (флаг включения + параметры)
E2E_RUN_LOAD=true E2E_LOAD_RPS=50 E2E_LOAD_EVENTS=500 \
  go test -v ./tests/suite/ -run TestLoadClosedLoop -timeout 30m
```

Переменные окружения и значения по умолчанию — [tests/README.md](../tests/README.md).

---

## Способ 3 — In-cluster (Job, «идеальный» режим)

Генератор и приёмник колбэков внутри кластера — трафик не выходит наружу.
Отчёт печатается **прямо в логи пода** как текст (`E2E_REPORT_FORMAT=text`) —
ни `kubectl cp`, ни веб-UI не нужны.

```bash
docker build -f tests/docker/Dockerfile -t webhook-tests:local .
# kind: kind load docker-image webhook-tests:local

kubectl apply -f tests/k8s/test-job.yaml
kubectl logs -n webhooks-test job/webhook-tests-functional -f          # онлайн
kubectl logs -n webhooks-test job/webhook-tests-functional > report.txt # сохранить
```

В `tests/k8s/test-job.yaml` есть Service, через DNS которого delivery-service
достучится до приёмника теста. URL сервисов внутри кластера правятся там же.
Нужен HTML — поставьте `E2E_REPORT_FORMAT=both` и заберите файл через `kubectl cp`.

---

## k6-путь (опционально) — моки + предзаведённые подписки

k6-скрипты (`tests/load/case5_load.js`, `case6_stress.js`) не принимают вебхуки,
поэтому доставки должны поглощать **моки** (`cmd/mock-receiver`), а подписки —
быть заведены заранее.

### Kubernetes

```bash
# Собрать образ мока
docker build -t mock-receiver:local ./cmd/mock-receiver

# Развернуть моки (отдельный namespace webhooks-test)
kubectl apply -f tests/k8s/mocks.yaml
kubectl wait --for=condition=ready pod -l app=mock-reliable -n webhooks-test --timeout=60s

# Зарегистрировать подписки для source load-test → моки
bash tests/k8s/mock-setup.sh
```

Три профиля хаоса:

| Мок | client_error% | error% | reset% | slow% | Назначение |
|-----|:---:|:---:|:---:|:---:|-----------|
| `mock-reliable` | 2 | 3 | 2 | 10 | Почти всегда 200 |
| `mock-flaky` | 5 | 15 | 10 | 20 | Умеренные ошибки |
| `mock-chaos` | 10 | 30 | 20 | 15 | Максимальный хаос |

- `client-error` → 400 (delivery-service не ретраит)
- `error` → 503 (exponential-backoff retry)
- `reset` → TCP RST (аналог таймаута, retry)

### Docker Compose

```bash
cd docker
docker compose -f docker-compose.yml -f docker-compose.stress.yml up -d --build
docker logs mock-setup   # дождаться "mock-setup done"
```

### Запуск k6

```bash
# через образ тестов
docker run --rm -v "$PWD/out:/reports" \
  -e E2E_EVENT_RECEIVER_URL=http://event-receiver.localhost \
  -e E2E_LOAD_RPS=200 -e E2E_LOAD_DURATION=10m \
  webhook-tests:local k6-load

# или локально установленным k6
k6 run -e E2E_EVENT_RECEIVER_URL=http://event-receiver.localhost \
       -e E2E_LOAD_RPS=200 tests/load/case5_load.js
```

### Очистка моков

```bash
kubectl delete -f tests/k8s/mocks.yaml && kubectl delete namespace webhooks-test
# или: cd docker && docker compose -f docker-compose.yml -f docker-compose.stress.yml down -v
```

---

## Наблюдение во время прогона

| Что смотреть | Local (k8s) | Staging |
|-------------|-------------|---------|
| Overview / Kafka Lag | http://grafana.localhost | http://grafana.staging.dws.sidey383.ru |
| Kafka UI (consumer lag) | http://kafka-ui.localhost | http://kafka-ui.staging.dws.sidey383.ru |
| Prometheus | http://prometheus.localhost | http://prometheus.staging.dws.sidey383.ru |

Autoscaling:
```bash
kubectl get scaledobject -n webhooks -w   # KEDA по Kafka lag
kubectl get hpa -n webhooks -w            # HPA event-receiver
```

> **Очистка подписок:** функциональные кейсы удаляют свои подписки автоматически
> (`t.Cleanup`). Отключается через `E2E_CLEANUP=false`. k6-путь использует
> долгоживущие подписки к мокам — их чистит `kubectl delete` / `compose down -v`.
