# Автоматизированные тесты системы доставки вебхуков

Набор **ручных, но автоматизированных** тестов: запускаются по команде тестировщиком,
полностью настраиваются через переменные окружения и генерируют **HTML-отчёты**.

| # | Кейс | Тест | Отчёт |
|---|------|------|-------|
| 1 | Успешная доставка | `suite/case1_delivery_test.go` | `functional.html` |
| 2 | Ретраи (500 → backoff → успех) | `suite/case2_retry_test.go` | `functional.html` |
| 3 | Частичный приём (часть подписчиков падает) | `suite/case3_partial_test.go` | `functional.html` |
| 4 | Множество подписчиков (fan-out) | `suite/case4_multi_subscriber_test.go` | `functional.html` |
| 5 | Смена подписки на лету (**PUT**) | `suite/case5_concurrent_change_test.go` | `functional.html` |
| 6 | Удаление подписки на лету (**DELETE**) | `suite/case6_delete_test.go` | `functional.html` |
| L | Нагрузка closed-loop (sink + сверка pending) | `suite/load_closed_test.go` → `TestLoadClosedLoop` | `load.html` |
| S | Стресс closed-loop (ступенчатый RPS) | `suite/load_closed_test.go` → `TestStressClosedLoop` | `stress.html` |
| k6 | Нагрузка по приёму (опционально) | `load/case5_load.js`, `load/case6_stress.js` | `k6-report.html` |

Кейсы 1-6 проверяют корректность и покрывают весь API подписок (POST/GET/PUT/DELETE).
Closed-loop load/stress поднимают **свой приёмник всех доставок** — меряют ingestion-
и delivery-latency, throughput и сверяют sent/delivered/**pending** (что «зависло»).
k6 — опциональный инструмент для детальных HTTP-перцентилей приёма (нужны моки, см. ниже).

---

## Два режима запуска

| Режим | Когда | Как |
|-------|-------|-----|
| **In-cluster** (идеальный) | Минимум сетевых издержек, колбэк внутри кластера | `kubectl apply -f k8s/test-job.yaml` |
| **Внешний** (реальный) | Серверы разнесены, трафик через Ingress | Docker / `go test` против публичного URL |

---

## Конфигурация (всё через ENV)

### Общие — куда стучать

| Переменная | Назначение | Дефолт |
|------------|-----------|--------|
| `E2E_EVENT_RECEIVER_URL` | URL сервиса приёма событий | `http://staging.dws.sidey383.ru` |
| `E2E_SUBSCRIPTIONS_URL` | URL API подписок | `http://subscriptions.dws.sidey383.ru` |
| `E2E_BASIC_AUTH_USER` / `E2E_BASIC_AUTH_PASS` | Basic Auth (пусто = не слать) | `admin` / *(пусто)* |
| `E2E_ACCEPT_STATUSES` | Коды «событие принято» | `200,202` |
| `E2E_REPORT_FORMAT` | `html` (файл) · `text` (в логи) · `both` | `html` |
| `E2E_CLEANUP` | `false` = не удалять подписки после прогона | `true` |
| `E2E_SKIP_IF_UNREACHABLE` | `true` = skip вместо fail если сервис недоступен | `false` |

### Колбэк (как delivery-service достучится до теста)

| Переменная | Назначение | Дефолт |
|------------|-----------|--------|
| `E2E_CALLBACK_HOST` | Хост, анонсируемый в подписках | `host.docker.internal` |
| `E2E_CALLBACK_PORT` | Порт колбэка | `8089` |
| `E2E_LISTEN_PORT` | Порт, который реально слушает тест | = `CALLBACK_PORT` |

> **Важно:** колбэк-приёмник теста должен быть **достижим со стороны
> delivery-service**. Локально через Docker — `host.docker.internal`; в кластере —
> DNS-имя Service (см. `k8s/test-job.yaml`); снаружи — публичный хост с
> проброшенным портом.

### Тайминги функциональных кейсов

| Переменная | Кейс | Дефолт |
|------------|------|--------|
| `E2E_DELIVERY_TIMEOUT` | ожидание доставки | `60s` |
| `E2E_RETRY_WAIT_TIMEOUT` | ожидание ретраев (кейс 2) | `90s` |
| `E2E_NO_RETRY_WINDOW` | окно «4xx не ретраится» (кейс 3) | `20s` |
| `E2E_MIN_RETRY_ATTEMPTS` | мин. попыток при 5xx (кейс 2) | `2` |
| `E2E_FANOUT` | число подписчиков (кейс 4) | `5` |
| `E2E_DRAIN_TIMEOUT` | ожидание доставок (кейсы 5, 6, load) | `60s` |

### Кейс 5 — смена подписки (PUT)

| Переменная | Назначение | Дефолт |
|------------|-----------|--------|
| `E2E_CHANGE_EVENTS` / `E2E_CHANGE_RPS` | объём и темп потока | `225` / `3` |
| `E2E_CHANGE_AT` | доля потока, на которой делается PUT (0..1) | `0.12` |
| `E2E_CHANGE_MAX_PROPAGATION` | SLO применения смены (bug-flag) | `5s` |

> На staging обнаружен кэш подписок в subscriptions-worker (~48s): PUT сразу
> персистится в API, но на доставку влияет с задержкой ~48s. Инвариант
> at-least-once при этом держится. Кейс помечает медленное применение bug-flag-
> проверкой (поднимите `E2E_CHANGE_MAX_PROPAGATION`, если задержка приемлема).

### Кейс 6 — удаление подписки (DELETE)

| Переменная | Назначение | Дефолт |
|------------|-----------|--------|
| `E2E_DELETE_EVENTS` / `E2E_DELETE_RPS` | объём и темп потока | `225` / `3` |
| `E2E_DELETE_AT` | доля потока, на которой делается DELETE | `0.12` |
| `E2E_DELETE_TAIL_FRACTION` | доля «хвоста» для проверки «удалённый молчит» | `0.2` |

### Нагрузка / стресс (closed-loop, Go)

| Переменная | Назначение | Дефолт |
|------------|-----------|--------|
| `E2E_RUN_LOAD` / `E2E_RUN_STRESS` | включить тест (entrypoint выставляет сам) | `false` |
| `E2E_LOAD_RPS` / `E2E_LOAD_EVENTS` | темп и объём (load) | `20` / `100` |
| `E2E_LOAD_FANOUT` | подписчиков на событие | `1` |
| `E2E_STRESS_START_RPS` → `E2E_STRESS_PEAK_RPS` | ступени стресса | `20` → `100` |
| `E2E_STRESS_STEP_EVENTS` | событий на ступень | `100` |

### k6 (опционально)

| Переменная | Назначение | Дефолт |
|------------|-----------|--------|
| `E2E_SOURCES` / `E2E_EVENT_TYPES` | источники/типы (через запятую) | `load-test` / `order.created` |
| `E2E_LOAD_RPS` / `E2E_LOAD_DURATION` | нагрузка | `100` / `5m` |
| `E2E_STRESS_START_RPS` / `E2E_STRESS_PEAK_RPS` / `E2E_STRESS_STAGE` | рампа | `50` / `1000` / `1m` |
| `E2E_THRESHOLD_P95_MS` / `E2E_THRESHOLD_P99_MS` / `E2E_THRESHOLD_ERROR_RATE` | SLO | `200` / `500` / `0.01` |

> k6-скрипты не принимают вебхуки — доставки должны поглощать моки
> (`cmd/mock-receiver`), а подписки для `E2E_SOURCES` — быть заведены заранее.
> Развёртывание моков: [../docs/03-testing.md](../docs/03-testing.md).

---

## Запуск

### Docker-образ (рекомендуется)

```bash
docker build -f tests/docker/Dockerfile -t webhook-tests:local .

# Функциональные кейсы 1-6 → out/functional.html
docker run --rm -v "$PWD/out:/reports" -p 8089:8089 \
  -e E2E_EVENT_RECEIVER_URL=http://staging.dws.sidey383.ru \
  -e E2E_SUBSCRIPTIONS_URL=http://subscriptions.staging.dws.sidey383.ru \
  -e E2E_BASIC_AUTH_USER=admin -e E2E_BASIC_AUTH_PASS=*** \
  -e E2E_CALLBACK_HOST=<хост-достижимый-из-кластера> -e E2E_CALLBACK_PORT=8089 \
  webhook-tests:local functional

# Closed-loop нагрузка / стресс
docker run --rm -v "$PWD/out:/reports" -p 8089:8089 -e ... webhook-tests:local load
docker run --rm -v "$PWD/out:/reports" -p 8089:8089 -e ... webhook-tests:local stress
```

Команды образа: `functional` · `load` · `stress` · `all` · `k6-load` · `k6-stress`.
Один кейс: добавьте `-test.run TestCase6_DeleteSubscription` в конец.

> **Windows + Git Bash:** монтируйте абсолютным путём с `MSYS_NO_PATHCONV=1`,
> иначе отчёт останется внутри контейнера и потеряется при `--rm`:
> ```bash
> MSYS_NO_PATHCONV=1 docker run --rm -v "C:/.../out:/reports" -p 8089:8089 ... webhook-tests:local functional
> ```
> **Windows + Docker Desktop:** для входящих колбэков в контейнер может
> потребоваться правило firewall на входящий порт (admin, один раз):
> ```powershell
> New-NetFirewallRule -DisplayName "webhook-tests inbound 8089-8094" `
>   -Direction Inbound -Action Allow -Protocol TCP -LocalPort 8089-8094 -Profile Any
> ```

### Нативно (без Docker)

```bash
# Функциональные → HTML
go test -json ./tests/suite/... | go run ./cmd/report-gen -out functional.html

# Один кейс
go test -v ./tests/suite/ -run TestCase2_RetryOn5xx

# Closed-loop нагрузка
E2E_RUN_LOAD=true E2E_LOAD_RPS=50 E2E_LOAD_EVENTS=500 \
  go test -v ./tests/suite/ -run TestLoadClosedLoop -timeout 30m
```

### In-cluster (Job)

В Job отчёт печатается **прямо в логи пода** как читаемый текст
(`E2E_REPORT_FORMAT=text` в `test-job.yaml`) — ни `kubectl cp`, ни веб-UI не нужны.

```bash
docker build -f tests/docker/Dockerfile -t webhook-tests:local .
kubectl apply -f tests/k8s/test-job.yaml

# смотреть онлайн
kubectl logs -n webhooks-test job/webhook-tests-functional -f
# сохранить отчёт
kubectl logs -n webhooks-test job/webhook-tests-functional > functional-report.txt
```

Нужен и HTML-файл? Поставьте `E2E_REPORT_FORMAT=both` и смонтируйте том /
`kubectl cp webhooks-test/<pod>:/reports/functional.html .`

---

## Отчёты

- **`functional.html`** — карточка на каждый кейс: статус, длительность, метрики
  (latency доставки, число ретраев, fan-out, задержка применения смены),
  проверки, шаги сценария, полный лог под спойлером. Генерирует `cmd/report-gen`
  из `go test -json`.
- **`load.html` / `stress.html`** — ingestion/delivery latency по перцентилям,
  throughput, и сверка `pending` (ничего не зависло).
- **`k6-report.html` / `k6-stress.html`** — плитки и таблица перцентилей приёма,
  статус порогов. Генерирует k6 в `handleSummary()` (офлайн, без внешних импортов).

Все HTML-отчёты — самодостаточные (открываются в браузере, годятся для презентации).

**Текстовый отчёт** (`E2E_REPORT_FORMAT=text`) — те же кейсы, проверки и метрики
в читаемом виде прямо в stdout/логи. Незаменимо для k8s Job, где файлы не вытащить:
`kubectl logs job/... > report.txt`. `both` — и файл, и текст в логи.

---

## Очистка

Функциональные кейсы удаляют свои подписки автоматически (`t.Cleanup` по `source`),
даже если тест упал. Отключается через `E2E_CLEANUP=false` (для разбора подписок
после прогона). k6-путь использует долгоживущие подписки к мокам — их чистит
`kubectl delete -f tests/k8s/mocks.yaml` / `docker compose ... down -v`.
