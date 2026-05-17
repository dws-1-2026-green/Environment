# Load Testing

## Что это

Нагрузочные тесты для EventReceiver. Отправляют события на `POST /sources/load-test/events` с фиксированным RPS в течение 10 минут. Доставка до вебхуков не проверяется — тест измеряет только приём событий.

Три мок-ресивера симулируют реальное поведение вебхук-эндпоинтов:

| Контейнер | Профиль | error+reset | slow (500ms-2s) | fast (<50ms) |
|-----------|---------|-------------|-----------------|--------------|
| `mock-reliable` | стабильный | 5% | 10% | 85% |
| `mock-flaky` | нестабильный | 25% | 20% | 55% |
| `mock-chaos` | хаотичный | 50% | 15% | 35% |

`reset` — обрыв TCP-соединения без ответа, delivery-service видит сетевую ошибку и уходит на retry с экспоненциальным backoff.

---

## Запуск

### 1. Поднять стек

```bash
cd Environment
docker compose -f docker-compose.yml -f docker-compose_stress_test.yml up -d --build
```

### 2. Дождаться регистрации подписок

```bash
docker logs mock-setup
```

Ожидаемый вывод:

```
waiting for subscriptions-api...
registered → mock-reliable
registered → mock-flaky
registered → mock-chaos
mock-setup done
```

Cassandra + subscriptions-api поднимаются ~30-60 секунд — `mock-setup` ждёт автоматически.

### 3. Проверить подписки (опционально)

```bash
curl -s http://localhost:8082/api/v1/subscriptions | grep load-test
```

### 4. Запустить тест

```bash
# из папки Environment
go test -v -run TestLoad_10RPS  -timeout 15m ./tests/e2e/
go test -v -run TestLoad_50RPS  -timeout 15m ./tests/e2e/
go test -v -run TestLoad_100RPS -timeout 15m ./tests/e2e/
```

Каждые 30 секунд в лог выводится статистика: sent / ok / err / actual rps.

### 5. Остановить стек

```bash
docker compose -f docker-compose.yml -f docker-compose_stress_test.yml down
```

---

## Повторный запуск

Подписки сохраняются в Cassandra (персистентный volume) — повторно регистрировать не нужно, `mock-setup` при рестарте создаст дубли. Для чистого старта:

```bash
docker compose -f docker-compose.yml -f docker-compose_stress_test.yml down -v
```

---

## Мониторинг во время теста

- **Grafana** — http://localhost:3000 (admin / admin)
- **Kafka UI** — http://localhost:8081
- **Prometheus** — http://localhost:9090

Логи моков в реальном времени:

```bash
docker logs -f mock-reliable
docker logs -f mock-flaky
docker logs -f mock-chaos
```

---

## Файлы

| Файл | Описание |
|------|----------|
| `docker-compose_stress_test.yml` | Overlay с моками и mock-setup |
| `cmd/mock-receiver/main.go` | Мок-сервер вебхуков |
| `cmd/mock-receiver/Dockerfile` | Сборка мок-сервера |
| `tests/e2e/e2e_load_test.go` | Тесты TestLoad_10/50/100RPS |
