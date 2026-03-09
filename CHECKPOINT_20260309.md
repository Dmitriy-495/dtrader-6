# DTrader 6 — Checkpoint 20250309

## Статус: В разработке 🚧

## Принятые архитектурные решения

### Микросервисы (все на Go)
- `bot`       — чистый сборщик сырых данных с биржи → Redis
- `ws-server` — Redis → WebSocket клиенты
- `ws-client` — временный клиент просмотра данных (Node.js)
- `strategy`  — дирижёр: loop_indicators → signal (позже)
- `trader`    — исполнение ордеров (позже)
- `risk`      — управление рисками (позже)

### Биржа
- Gate.io USDT Perpetual Futures
- WS: wss://fx-ws.gateio.ws/v4/ws/usdt
- REST: https://api.gateio.ws/api/v4

### Торгуемые пары
- BTC_USDT, ETH_USDT, SOL_USDT

### Таймфреймы
- Bot собирает: 1m свечи, тики, стакан (depth 20)
- Стратегия сама агрегирует нужные таймфреймы
- HTF/MTF/LTF — специфика стратегии, не бота

### Redis — единственный источник истины
- candles:1m:{symbol}      — последние 200 свечей
- trades:{symbol}          — последние 1000 тиков
- orderbook:{symbol}:bids  — стакан bid 20 levels
- orderbook:{symbol}:asks  — стакан ask 20 levels

### Стратегия (позже)
- Дирижёр + индикаторы-музыканты
- loop_indicators() внутри стратегии
- Nginx-style: strategies/available + strategies/enabled
- Конфиг перечитывается на maintenance window (02:00 и 14:00)

### Maintenance Window
- 02:00 и 14:00 по времени биржи Gate.io
- Trader останавливается
- Bot и индикаторы продолжают работу
- Flush Redis → PgSQL, GC, reload конфига

### Безопасность
- Секреты только в .env (не в Git!)
- ws-server токен авторизации: ?token=<WS_SECRET_TOKEN>
- HMAC-SHA512 подпись каждого приватного запроса

## Стек технологий
| Компонент | Версия |
|---|---|
| Go | 1.22.3 |
| Redis | 6.0.16 |
| Node.js | 24.11.1 |
| go-redis/v9 | v9.18.0 |
| gorilla/websocket | v1.5.3 |
| go.yaml.in/yaml/v3 | v3.0.4 |
| joho/godotenv | v1.5.1 |

## Готовые файлы
- [x] .gitignore
- [x] README.md
- [x] bot/config.yaml
- [x] bot/.env (не в Git)
- [x] bot/go.mod + go.sum
- [x] bot/internal/config/config.go
- [x] bot/internal/utils/hmac.go
- [x] bot/internal/utils/time.go
- [x] bot/internal/utils/http.go
- [x] ws-server/config.yaml
- [x] ws-server/.env (не в Git)
- [x] ws-server/go.mod + go.sum
- [x] ws-client/package.json

## В работе
- [ ] bot/internal/gateway/client.go
- [ ] bot/internal/gateway/rest.go
- [ ] bot/cmd/main.go

## Позже
- [ ] bot/internal/publisher/ (Redis)
- [ ] bot/internal/gateway/ws.go (WebSocket)
- [ ] ws-server (полная реализация)
- [ ] ws-client (полная реализация)
- [ ] strategy сервис
- [ ] trader сервис
- [ ] risk сервис
