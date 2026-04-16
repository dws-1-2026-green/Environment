# Конфигурация для запуска

Этот репозиторий содержит Docker Compose конфигурацию для быстрого развертывания окружения разработки сервисов со зависимостями (Kafka, PostgreSQL, Kafka UI).

# Быстрый старт
1) Клонируйте репозиторий

```
git clone git@github.com:dws-1-2026-green/Environment.git
cd Environment
```
2) Запустить все сервисы
```
docker-compose up -d
```
# Ссылки на сервисы
- [Subscriptions](https://github.com/dws-1-2026-green/subscriptions)
- [EventReceiver](https://github.com/dws-1-2026-green/EventReceiver)

# Доступ к сервисам для тестов
# Kafka
Брокер сообщений с хоста `localhost:9092`

Брокер сообщений из docker контейнеров `kafka:9094`

UI для управления `http://localhost:8081`

# PostgreSQL

- Host: `localhost`
- Port: `5432`
- User: `green`
- Password: `green-password`
- Database: `green`
