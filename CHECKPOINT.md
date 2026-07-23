# DTrader 6 — Полный чекпоинт системы (2026-07-22)

## ДЕВИЗ
**"ПОРВЁМ GATE.IO К ЧЕРТЯМ СОБАЧЬИМ!"** 🔥

---

## 1. ОБЩАЯ КОНЦЕПЦИЯ СИСТЕМЫ

### Целевая архитектура (микросервисы через Redis)
                Gate.io WebSocket/REST
                      ↓
               [market-data]  ← бывший "bot"
                      ↓

╔═══════════════════════ REDIS ════════════════════════╗
║ шина данных между всеми сервисами ║
╚══════════════════════════════════════════════════════╝
↓ ↓ ↓ ↓
[analyzer] [position-tracker] [ws-server] ...
↓
[signal-engine] ← TVP-Sniper
↓
[risk-guard]
↓
[executor] ──→ Gate.io REST (ордера)
↓
[position-tracker]
↓
Redis
↓
[ws-server] ──→ [TUI] (локальный клиент)


### Таблица сервисов
| ID | Имя | Бывшее имя | Статус | Роль |
|---|---|---|---|---|
| A | market-data | bot | ✅ Работает (корявo) | Gate.io → Redis |
| B | executor | trader | ⬜ Планируется | Сигналы → Ордера Gate.io |
| C | signal-engine | strategies | ⬜ Планируется | Индикаторы → Сигналы TVP-Sniper |
| D | analyzer | indicators | ⬜ Планируется | Поток данных → Индикаторы |
| E | risk-guard | risk-manager | ⬜ Планируется | Фильтрация сигналов, защита капитала |
| F | ws-server | ws-server | ✅ Работает | Redis → WebSocket → TUI |
| G | position-tracker | — | ⬜ Планируется | Позиции, P&L реальный |
| Z | Redis | Redis | ✅ Работает | Шина данных |

---

## 2. ТОРГОВАЯ КОНЦЕПЦИЯ

### Стратегия TVP-Sniper (1m, 8m, 24m)
- **T** — мульти таймфреймы (1m, 8m, 24m) — подтверждение тренда на нескольких ТФ
- **V** — объёмы (рост давления покупок/продаж)
- **P** — давление в стакане (order book imbalance)
- **Sniper** — точный вход. 200ms латентность некритична для 1m свечей.

### Управление позициями — "Всегда в рынке"

ВХОД ЛОНГ: T↑ + V↑ + P(buyers) → открыть LONG
ВЫХОД ЛОНГ: сигнал разворота (T↓ + V↑ + P(sellers))
ВХОД ШОРТ: немедленно после закрытия лонга
ВЫХОД ШОРТ: сигнал разворота → вход в лонг

- **НЕТ** классических стоп-лоссов как основного механизма выхода
- **НЕТ** классических тейк-профитов
- Выход ТОЛЬКО по сигналу разворота тренда
- Цель — всегда быть в позиции, максимально использовать тренд

### Защитные стопы (форс-мажор)
- Стопы ЕСТЬ но только как аварийный тормоз:
  - сильное проскальзывание
  - технические сбои системы
  - flash crash / экстремальная волатильность
- Выставляются на **значительной дистанции** от цены входа
- НЕ являются основным механизмом управления позицией

### Risk-guard логика
- Контроль размера позиции (% от депозита)
- Максимальная просадка за сессию
- Дневной лимит убытка → принудительный выход
- Аварийный стоп при экстремальном движении (>N% за M минут)

---

## 3. ОКРУЖЕНИЕ

### VDS (продакшн) — vm-tda495

OS: Ubuntu 22.04.5 LTS
Go: 1.22.3
Redis: 6.0.16 (localhost:6379)
PgSQL: 14.22 (user=dtrader, db=dtrader6)
IP: 88.218.67.93
Путь: /home/tda495/code/dtrader/dtrader-6
Хостинг: console.cloud.ru


### Локальные машины (разработка)

OS: Ubuntu 22, zsh
Go: 1.22.3
Терминал: Kitty
Путь TUI: /home/tda/code/dtrader/dtrader-tui-6
Путь bot: /home/tda/code/dtrader/dtrader-6


### Алиас запуска TUI (~/.zshrc)
```bash
alias tui='cd ~/code/dtrader/dtrader-tui-6 && go build -o ./bin/tui ./cmd/main.go && ./bin/tui'
export PATH=$PATH:$(go env GOPATH)/bin
```

---

## 4. РЕПОЗИТОРИИ

github.com/Dmitriy-495/dtrader-6 ветка master (bot + ws-server)
github.com/Dmitriy-495/dtrader-tui-6 ветка main (TUI, ПУБЛИЧНЫЙ)


---

## 5. УПРАВЛЕНИЕ СЕРВИСАМИ НА VDS

### Systemd сервисы
```bash
# Статус
sudo systemctl status dtrader-bot dtrader-ws dtrader-watcher

# Перезапуск
sudo systemctl restart dtrader-bot dtrader-ws dtrader-watcher

# Логи
sudo journalctl -u dtrader-bot -f
sudo journalctl -u dtrader-ws -f
sudo journalctl -u dtrader-watcher -n 20 --no-pager
```

### Деплой
```bash
cd ~/code/dtrader/dtrader-6
./deploy.sh "commit message"
# watcher.sh на VDS подхватывает через ~30s
# умный watcher перезапускает только изменившийся сервис
```

---

## 6. REDIS СХЕМА

### Текущие ключи
| Ключ | Тип | TTL | Содержимое |
|---|---|---|---|
| `market:trades:{symbol}` | Stream | — | тики: price, size, ts |
| `market:orderbook:{symbol}` | String | — | JSON снапшот стакана |
| `market:candles:1m:{symbol}` | List | — | закрытые свечи (макс 200) |
| `market:liquidations:{symbol}` | Stream | — | ликвидации |
| `market:stats:{symbol}` | String | — | JSON: lsr_taker, open_interest_usd |
| `system:exchange_ping` | String | 60s | JSON: {"current":X,"ema":Y} RTT биржи |
| `account:balance` | String | — | JSON: {"total","margin","leverage"} |

### Планируемые ключи (будущие сервисы)
| Ключ | Сервис | Содержимое |
|---|---|---|
| `indicators:ema:{tf}:{symbol}` | analyzer | EMA по таймфреймам |
| `indicators:volume:{tf}:{symbol}` | analyzer | объёмное давление |
| `signals:entry:{symbol}` | signal-engine | сигналы входа |
| `positions:current` | position-tracker | открытые позиции |
| `positions:pnl` | position-tracker | P&L реальный |

---

## 7. ПРОТОКОЛ ws-server → TUI

```json
// Heartbeat каждые 10 секунд
{"channel":"system","symbol":"","data":{
  "server_ts": 1773359082497,
  "exchange_ping": {"current": 222, "ema": 288},
  "balance": {"total":"25.27","margin":"0","leverage":"3"}
}}

// Агрегированные трейды каждые 500ms
{"channel":"trades","symbol":"BTC_USDT","data":{
  "buy_vol": 1234.5, "sell_vol": 987.3,
  "buy_count": 15, "sell_count": 12,
  "last_price": "70500.5", "ts": 1773359082497
}}

// Статистика (при изменении)
{"channel":"stats","symbol":"BTC_USDT","data":{
  "lsr_taker": 1.25, "open_interest_usd": 4250000000
}}

// Ликвидации (при появлении)
{"channel":"liquidations","symbol":"BTC_USDT","data":{
  "price":"70000","size":"10","time_ms":1773359082497
}}

// Свечи (при закрытии 1m свечи)
{"channel":"candles","symbol":"BTC_USDT","data":{...}}
```

---

## 8. СТРУКТУРА ПРОЕКТА dtrader-6

dtrader-6/
├── bot/ ← будет: market-data/
│ ├── cmd/main.go — точка входа, цикл реконнекта
│ └── internal/
│ ├── config/config.go — .env: GATE_API_KEY, GATE_SECRET, REDIS_URL
│ ├── gateway/
│ │ ├── ws.go — WS клиент, ping/pong, EMA латентности
│ │ │ emaAlpha=2/101, pingTs, emaLat float64
│ │ ├── subscribe.go — подписки на каналы Gate.io futures
│ │ ├── rest.go — REST: получение баланса аккаунта
│ │ ├── client.go — фабрика WS клиента
│ │ └── constants.go — URL, названия каналов
│ ├── publisher/
│ │ └── redis.go — запись данных в Redis
│ └── utils/
│ ├── hmac.go — подпись запросов
│ ├── http.go — HTTP утилиты
│ └── time.go — время unix
├── ws-server/
│ ├── cmd/main.go — точка входа ws-server
│ └── internal/
│ ├── config/config.go — порт 9000, символы, redis
│ ├── hub/hub.go — менеджер WS клиентов (broadcast)
│ ├── reader/redis.go — чтение Redis, агрегация trades 500ms
│ │ heartbeat 10s, broadcastSystem
│ └── handler/ws.go — WS handler, аутентификация по API ключу
├── watcher.sh — умный деплой: перезапускает только изменившийся сервис
├── deploy.sh — git push → watcher
└── CHECKPOINT.md — этот файл


---

## 9. СТРУКТУРА ПРОЕКТА dtrader-tui-6

dtrader-tui-6/
├── cmd/main.go — точка входа
├── internal/
│ ├── config/config.go — .env: WS_SERVER_URL, WS_API_KEY, CRYPTOPANIC_API_KEY
│ ├── news/client.go — RSS Cointelegraph RU (каждые 5 мин)
│ ├── ws/client.go — WebSocket клиент с автореконнектом
│ └── ui/
│ ├── app.go — главная Model (оркестратор bubbletea)
│ ├── styles.go — ВСЕ стили: orange=214, borders оранжевые
│ ├── header.go — ⚡ DTrader 6 | время | баланс | PnL | SERV | EXCH | ⚙
│ ├── footer.go — командная строка
│ ├── layout.go — renderMain: tabs + [content|rightbar]
│ ├── tabs.go — powerline вкладки с оранжевыми border
│ ├── news.go — RSS лента новостей (синий текст)
│ ├── rightbar.go — стили Logs и Positions
│ ├── sidebar.go — addLog()
│ ├── settings.go — иконка ⚙ (заглушка, будет модалка)
│ └── screens/
│ ├── dashboard.go — 📊 таблица: пара/цена/buy_vol/sell_vol/LSR/OI
│ └── pair.go — детальный экран пары
├── .env — секреты (НЕ в git!)
└── CHECKPOINT.md


---

## 10. .ENV ФАЙЛЫ

### dtrader-6/bot/.env (VDS и локалка)

GATE_API_KEY=...
GATE_SECRET=...
REDIS_URL=localhost:6379


### dtrader-tui-6/.env (локалка)

WS_SERVER_URL=ws://88.218.67.93:9000/ws
WS_API_KEY=dtrader6_ws_secret
CRYPTOPANIC_API_KEY=79f2be56e48ea3978d8992bcd57791c14554a505


---

## 11. ДИЗАЙН-СИСТЕМА TUI

Фирменный цвет: оранжевый lipgloss.Color("214")
Все рамки: оранжевые colorBorder="214"
Статус OK: зелёный "82"
Статус WARNING: жёлтый "226"
Статус SOS/OFF: красный "196"
Текст важный: белый "255"
Текст данные: оранжевый "214"
Текст вспомог.: серый "239"
Новости: синий "39"


### Header (3 строки с рамкой)

╭─────────────────────────────────────────────────────────────────╮
│ ⚡ DTrader 6 09:19 UTC 💰$25.27 ↑+$0.17 ↑+$2.43 ●SERV ●EXCH ⚙│
╰─────────────────────────────────────────────────────────────────╯


### Индикаторы
- **SERV**: зелёный <100ms, жёлтый ≥100ms, красный OFF
- **EXCH**: зелёный <300ms, жёлтый 300-1000ms, красный ≥1000ms SOS

### Горячие клавиши
| Клавиша | Действие |
|---|---|
| Tab / Shift+Tab | следующая/предыдущая вкладка |
| Ctrl+1..5 | прямой переход к вкладке |
| Ctrl+C | выход |

---

## 12. EMA ЛАТЕНТНОСТИ

α = 2/(100+1) ≈ 0.0198
EMA = current × α + prev_EMA × (1-α)
Инициализация: первым значением (emaLat == 0 → emaLat = current)
Ping интервал: 10s
Redis ключ: system:exchange_ping → {"current": X, "ema": Y}


---

## 13. ПЛАН РЕФАКТОРИНГА (следующий чат)

### Приоритет 1 — market-data (bot)
Текущие проблемы:
- `gateway/ws.go` — монолит 300+ строк, делает всё
- Нет обработки ошибок в publisher
- EMA логика в gateway (лучше вынести в metrics/)
- Нет структурированного логирования (нужен slog)
- Нет graceful shutdown

Целевая структура:

bot/internal/
├── config/
├── gateway/
│ ├── connection.go — только WS соединение
│ ├── pingloop.go — только ping/pong
│ ├── parser.go — парсинг сообщений по каналам
│ └── subscribe.go — подписки
├── metrics/
│ └── ema.go — EMA латентности
├── publisher/
│ └── redis.go — с обработкой ошибок
└── utils/


### Приоритет 2 — ws-server
- reader/redis.go — разбить по файлам (trades.go, stats.go, system.go)
- Добавить graceful shutdown
- Улучшить обработку переподключений клиентов

### Приоритет 3 — TUI layout
- Финальное выравнивание (правые borders ±1 символ)
- Сброс buy/sell vol каждую минуту
- Реальный P&L из position-tracker

### Приоритет 4 — новые сервисы
1. analyzer (индикаторы)
2. signal-engine (TVP-Sniper)
3. risk-guard
4. executor
5. position-tracker

---

## 14. БЕЗОПАСНОСТЬ (PENDING)
- Закрыть порт 9000 до конкретных IP в console.cloud.ru
- Группа безопасности: SSH-access_ru.AZ-2
- PostgreSQL доступен только с localhost
