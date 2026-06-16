# План нагрузочного тестирования

> Запустить тесты прямо сейчас: [03-testing.md](03-testing.md).
> Развернуть стек: [02-deploy.md](02-deploy.md).

## Цель

Исследовать поведение системы под нагрузкой: найти узкие места, научиться их
видеть в метриках и логах, понять как их устранять. Не просто "держим X RPS",
а "понимаем что происходит когда перестаём держать".

---

## Уровни нагрузки

| Уровень | RPS    | Ожидание |
|---------|--------|----------|
| Baseline | 100   | Система в покое, все метрики в норме |
| Low      | 1,000 | Комфортная работа, latency стабильна |
| Medium   | 5,000 | Начало интересного: Kafka lag, рост latency |
| High     | 10,000 | Узкие места проявляются явно |
| Stress   | 50,000 | Система под предельной нагрузкой |

---

## Инструменты генерации нагрузки

### Вариант A — локально (5k RPS и ниже)

```bash
go install github.com/rakyll/hey@latest

hey -z 60s -c 200 -m POST \
  -H "Content-Type: application/json" \
  -d '{"id":"evt-1","type":"load.event","data":{"x":1}}' \
  http://event-receiver.localhost/sources/load-test/events
```

`-c` — количество параллельных воркеров. Увеличивай постепенно.
Ограничение: генератор и сервер делят CPU одной машины — цифры будут занижены.

### Вариант B — k6 (гибче, встроенные метрики)

```javascript
// tests/load/k6-script.js
import http from 'k6/http';
import { sleep } from 'k6';

export const options = {
  stages: [
    { duration: '1m', target: 100  },  // разогрев
    { duration: '2m', target: 1000 },  // low
    { duration: '2m', target: 5000 },  // medium
    { duration: '2m', target: 10000 }, // high
    { duration: '1m', target: 0    },  // остывание
  ],
};

export default function () {
  http.post(
    'http://event-receiver.localhost/sources/load-test/events',
    JSON.stringify({ id: `evt-${__VU}-${__ITER}`, type: 'load.event', data: { x: 1 } }),
    { headers: { 'Content-Type': 'application/json' } }
  );
}
```

```bash
k6 run tests/load/k6-script.js
```

### Вариант C — облачный генератор (для честных 10k-50k RPS)

Чтобы нагрузка не конкурировала с сервером за CPU:

**k6 Cloud** (бесплатный tier):
- Пишешь скрипт локально, нагрузку генерируют серверы Grafana
- Нужен staging-кластер с внешним IP (`k8s/overlays/staging/`)
- `k6 cloud tests/load/k6-script.js`

**Отдельная VM** (EC2 c5.xlarge ~$0.17/час):
- Берёшь на час, ставишь k6 или hey
- Натравливаешь на staging
- Честная изоляция генератора от сервера

---

## Где ожидать узкие места и как их видеть

### 1. Event Receiver — HTTP ingestion

**Что сломается первым**: CPU на сериализацию + Kafka producer backpressure.

**Как видеть в Grafana**:
- Дашборд "Event Receiver" → панель HTTP Requests (rate/sec) — рост
- Latency p99 начнёт расти нелинейно
- Events Published (error) — появятся ошибки публикации в Kafka

**Как решать**:
- Увеличить `replicas` в `k8s/base/apps/event-receiver.yaml`
- Увеличить партиции `routing.requests` под количество реплик

---

### 2. Kafka — узкое горлышко по партициям

**Что сломается**: при 1 партиции только 1 консюмер получает сообщения.
С 2 партициями — 2 консюмера. Число партиций = потолок параллелизма.

**Как видеть в Grafana**:
- Дашборд "Overview" → Kafka Lag: routing.requests / deliveries.to_send
- Lag начинает расти → консюмеры не успевают

**Как решать**:
```bash
# Увеличить партиции (только вверх): отредактируй spec.partitions у KafkaTopic
# в k8s/base/messaging/kafka.yaml — Strimzi topic operator применит изменение.
# Текущее состояние можно посмотреть так (под Strimzi: kafka-dual-role-0):
kubectl exec -n webhooks kafka-dual-role-0 -- /opt/kafka/bin/kafka-topics.sh \
  --bootstrap-server kafka-kafka-bootstrap:9092 \
  --describe --topic deliveries.to_send
```
- Увеличить реплики потребителя (delivery-service) под новое число партиций

---

### 3. Subscriptions Worker — поиск подписок в БД

**Что сломается**: при высоком RPS worker читает из Kafka быстро,
но каждое событие → запрос к PostgreSQL. БД становится узким местом.

**Как видеть в Grafana**:
- Дашборд "Subscriptions" → DB Query Duration — p99 растёт
- Kafka Lag: routing.requests растёт (worker не успевает)

**Как решать**:
- Увеличить `replicas` subscriptions-worker
- Добавить индекс (уже есть `idx_subscriptions_lookup`)
- Добавить in-process кэш подписок (сейчас не реализовано)

---

### 4. Delivery Service — HTTP доставка на вебхуки

**Что сломается**: при большом fan-out (много подписок на событие)
очередь `deliveries.to_send` растёт быстрее чем delivery-service её разгребает.

**Как видеть в Grafana**:
- Дашборд "Delivery Service" → Pending Deliveries — растёт
- Kafka Lag: deliveries.to_send — растёт
- Attempt Duration p99 — растёт (вебхук-получатели начинают тормозить)

**Как решать**:
- Увеличить `replicas` delivery-service
- Увеличить партиции `deliveries.to_send` под число реплик
- Тюнинг таймаутов HTTP-клиента

---

### 5. PostgreSQL (delivery-service store)

**Что сломается**: при высоком RPS каждая доставка → INSERT + UPDATE в БД.
При 10k+ deliveries/sec PostgreSQL станет узким местом.

**Как видеть**:
- `kubectl exec -n webhooks delivery-postgres-0 -- psql -U ds -d delivery -c "SELECT count(*) FROM deliveries WHERE status='pending';"`
- Метрик PostgreSQL пока нет в Grafana (TODO: добавить postgres_exporter)

**Как решать**:
- Батчинг записей (сейчас каждая доставка пишется отдельно)
- Async writes — писать в БД в фоне, не блокируя delivery

---

## Сценарий исследования (step-by-step)

1. Запустить стек (`kubectl apply -k k8s/overlays/local`)
2. Запустить моки (`kubectl apply -f tests/k8s/mocks.yaml`)
3. Зарегистрировать подписки (`bash tests/k8s/mock-setup.sh`)
4. Открыть Grafana: http://grafana.localhost
5. Запустить генератор с минимальной нагрузкой (100 RPS), убедиться что всё работает
6. Постепенно поднимать нагрузку, на каждом уровне:
   - Смотреть Kafka Lag в Overview
   - Смотреть Pending Deliveries в Delivery Service
   - Смотреть latency в Event Receiver
   - Записывать при каком RPS что сломалось
7. Применять фикс (больше реплик / больше партиций), убеждаться что помогло
8. Идти на следующий уровень

---

## TODO

- [ ] Написать k6-скрипт с поэтапным нарастанием нагрузки
- [ ] Добавить postgres_exporter в k8s для метрик БД
- [ ] Настроить staging окружение для честных тестов с внешним генератором
- [ ] Исследовать поведение при fan-out > 1 (несколько подписок на одно событие)
- [ ] Добавить алерты в Grafana на Kafka Lag > N и Pending Deliveries > N
