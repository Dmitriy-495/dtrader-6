# DTrader 6 — Чекпоинт v0.4 (2026-03-13)

## Репозиторий
- github.com/Dmitriy-495/dtrader-6 (ветка master)

## Статус микросервисов
| Сервис | Роль | Статус |
|---|---|---|
| `bot` | Биржа Gate.io → Redis | ✅ Работает |
| `ws-server` | Redis → WebSocket клиенты | ✅ Работает |
| `dtrader-tui-6` | WS клиент TUI | ✅ Работает в Kitty |
| `indicator-engine` | Redis raw → Indicators | ⬜ Следующий |
| `strategy` | Indicators → Signals (TVP-Sniper) | ⬜ |
| `trader` | Signals → Orders | ⬜ |

## Redis схема
| Ключ | Тип | Содержимое |
|---|---|---|
| `market:trades:{symbol}` | Stream | тики сделок |
| `market:orderbook:{symbol}` | String | снапшот стакана |
| `market:candles:1m:{symbol}` | List | закрытые свечи (200 макс) |
| `market:liquidations:{symbol}` | Stream | ликвидации |
| `market:stats:{symbol}` | String | OI, LSR, stats |
| `system:exchange_ping` | String | JSON {"current":X,"ema":Y} TTL 60s |
| `account:balance` | String | JSON {"total","margin","leverage"} |

## Протокол ws-server → TUI
```json
{"channel":"system","data":{"server_ts":...,"exchange_ping":{"current":222,"ema":288},"balance":{"total":"25.27","margin":"0","leverage":"3"}}}
{"channel":"trades","symbol":"BTC_USDT","data":{"buy_vol":...,"sell_vol":...,"last_price":...}}
{"channel":"orderbook","symbol":"BTC_USDT","data":{...}}
{"channel":"stats","symbol":"BTC_USDT","data":{...}}
{"channel":"candles","symbol":"BTC_USDT","data":{...}}
{"channel":"liquidations","symbol":"BTC_USDT","data":{...}}
```

## EMA латентности
- α = 2/(100+1) ≈ 0.0198
- Ping интервал: 10s
- Heartbeat ws-server: 10s

## PENDING
1. Dashboard — строки по парам (цена, объём, LSR, OI) — СЛЕДУЮЩИЙ
2. PnL в header (всего + за день)
3. indicator-engine микросервис
4. WS приватная аутентификация + futures.balances
5. Закрыть порт 9000 до конкретных IP
