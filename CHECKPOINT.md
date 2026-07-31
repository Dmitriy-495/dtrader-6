# DTrader 6 — Полный чекпоинт системы (2026-07-25)

## ДЕВИЗ

**"ПОРВЁМ GATE.IO К ЧЕРТЯМ СОБАЧЬИМ!"** 🔥

---

## 1. ОБЩАЯ КОНЦЕПЦИЯ СИСТЕМЫ

### Целевая архитектура (микросервисы через Redis)

```
 Gate.io WebSocket/REST
       ↓
[market-data]  ← бывший "bot"
       ↓
╔═══════════════════════ REDIS ════════════════════════╗
║              шина данных между всеми сервисами         ║
╚══════════════════════════════════════════════════════╝
   ↓            ↓                  ↓              ↓
[analyzer]  [position-tracker]  [ws-server]       ...
   ↓
[signal-engine]  ← TVP-Sniper
   ↓
[risk-guard]
   ↓
[executor]  ──→ Gate.io REST (ордера)
   ↓
[position-tracker]
   ↓
  Redis
   ↓
[ws-server]  ──→ [TUI] (локальный клиент)
```

### Таблица сервисов

| ID  | Имя              | Бывшее имя   | Статус                          | Роль                                 |
| --- | ---------------- | ------------ | ------------------------------- | ------------------------------------ |
| A   | market-data      | bot          | ✅ Отрефакторен, в бою на 2 VDS | Gate.io → Redis                      |
| B   | executor         | trader       | ⬜ Планируется                  | Сигналы → Ордера Gate.io             |
| C   | signal-engine    | strategies   | ⬜ Планируется                  | Индикаторы → Сигналы TVP-Sniper      |
| D   | analyzer         | indicators   | 🔶 Следующий в разработке       | Поток данных → Индикаторы            |
| E   | risk-guard       | risk-manager | ⬜ Планируется                  | Фильтрация сигналов, защита капитала |
| F   | ws-server        | ws-server    | ✅ Работает                     | Redis → WebSocket → TUI              |
| G   | position-tracker | —            | ⬜ Планируется                  | Позиции, P&L реальный                |
| Z   | Redis            | Redis        | ✅ Работает (на каждом VDS)     | Шина данных                          |

---

## 2. ТОРГОВАЯ КОНЦЕПЦИЯ

### Стратегия TVP-Sniper (1m, 8m, 24m)

- **T** — мульти таймфреймы (1m, 8m, 24m) — подтверждение тренда на нескольких ТФ
- **V** — объёмы (рост давления покупок/продаж)
- **P** — давление в стакане (order book imbalance)
- **Sniper** — точный вход. 200ms латентность некритична для 1m свечей.

### Управление позициями — "Всегда в рынке"

```
ВХОД ЛОНГ:  T↑ + V↑ + P(buyers)  → открыть LONG
ВЫХОД ЛОНГ: сигнал разворота (T↓ + V↑ + P(sellers))
ВХОД ШОРТ:  немедленно после закрытия лонга
ВЫХОД ШОРТ: сигнал разворота → вход в лонг
```

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

### ⚠️ Инфраструктура изменилась: было 1 VDS → стало 2 VDS

Причина: локальная машина (Россия) показала нестабильный доступ до
`api.gateio.ws` (AWS ELB, регион `ap-northeast-1`, Токио) — то полное
молчание на HTTP-уровне при живом TCP+TLS, то обрыв на TLS handshake.
VDS до того же домена достаёт стабильно. Диагностика подробно описана
в истории чата "market-data refactor"; резидентных VPN-хуков на
локалке не найдено — сделан вывод, что разработка ведётся локально,
а любой прогон с реальным подключением к бирже — только на VDS.

### VDS #1 — msk (pre-prod / резервный)

```
Хостинг:  Cloud.ru
IP:       91.224.87.61
OS:       Ubuntu 22.04.5 LTS
SSH алиас: msk
Латентность до Gate.io (REST): ~70-234ms в замерах, ранее фиксировалась
                                нестабильность до 1.7s — играет роль
                                текущей загрузки сети, не константа
Роль:     pre-prod — дальнейшее тестирование новых версий бота,
          dry-run/paper-trading режим (когда появится исполнение
          ордеров), проверка TUI, ДО того как катить на sgp
```

### VDS #2 — sgp (prod / боевой)

```
Хостинг:  JustHost Asia
IP:       185.229.222.77
OS:       Ubuntu 22.04.5 LTS
SSH алиас: sgp
Латентность до Gate.io (REST): ~70ms, стабильно — заметно лучше msk
Роль:     боевой сервер — сюда катим только после подтверждения
          на pre-prod (msk)
```

### Стек на каждом VDS

```
Go:     1.22.3
Redis:  localhost-only, requirepass с РАЗНЫМИ паролями на каждом сервере
        (компрометация одного пароля не даёт доступа к другому серверу)
systemd: dtrader-bot.service, dtrader-ws.service — Restart=on-failure
UFW:    открыты только 22 (SSH) и 9000 (ws-server, публичные данные,
        защищён WS_API_KEY)
```

### Структура на серверах

```
~/dtrader-6/
├── bin/
│   ├── bot/          — бинарник dtrader-bot + его config.yaml
│   │                    (СВОЯ рабочая директория — bot грузит
│   │                     config.yaml по относительному пути!)
│   └── ws-server/    — бинарник dtrader-ws + его config.yaml
├── shared/config/
│   ├── bot.env        — GATE_API_KEY, GATE_API_SECRET, REDIS_PASSWORD
│   │                     (НЕ в git; подключается через systemd
│   │                      EnvironmentFile=)
│   └── ws-server.env  — WS_API_KEY, REDIS_PASSWORD (НЕ в git)
└── logs/
    ├── bot.log / bot.error.log
    └── ws.log / ws.error.log
```

⚠️ **Важный урок из практики (пароли Redis):** `EnvironmentFile=` в
systemd читается ТОЛЬКО в момент старта процесса. Если поменять
`bot.env` вручную, но не перезапустить `dtrader-bot`, работающий
процесс продолжит жить со старым паролем в памяти, а файл на диске
уже будет другим — рассинхрон, который трудно диагностировать через
обычный `cat`/`grep` файла. Способ проверить, каким паролем реально
живёт запущенный процесс:

```bash
sudo systemctl show dtrader-bot -p MainPID   # получить PID
sudo cat /proc/<PID>/environ | tr '\0' '\n' | grep REDIS_PASSWORD
```

После любой правки `.env` — обязательно `sudo systemctl restart dtrader-bot`.

### Локальные машины (разработка)

```
OS:        Ubuntu 22, zsh, Kitty terminal
Go:        1.22.3
Путь TUI:  ~/code/dtrader/dtrader-tui-6
Путь bot:  ~/code/dtrader/dtrader-6
```

Разработчик (Дмитрий) ведёт разработку на разных локальных машинах,
синхронизация — через git. Реальное подключение к Gate.io тестируется
только на VDS (см. причину выше), локально — только `go build`/`go vet`.

### Алиас запуска TUI (~/.zshrc)

```bash
alias tui='cd ~/code/dtrader/dtrader-tui-6 && go build -o ./bin/tui ./cmd/main.go && ./bin/tui'
export PATH=$PATH:$(go env GOPATH)/bin
```

---

## 4. РЕПОЗИТОРИИ

```
github.com/Dmitriy-495/dtrader-6      ветка master (bot + ws-server)
github.com/Dmitriy-495/dtrader-tui-6  ветка main   (TUI, ПУБЛИЧНЫЙ)
```

---

## 5. УПРАВЛЕНИЕ СЕРВИСАМИ НА VDS

### Systemd сервисы (на каждом из двух VDS: msk и sgp)

```bash
# Статус
sudo systemctl status dtrader-bot dtrader-ws

# Перезапуск (обязателен после правки .env — см. раздел 3!)
sudo systemctl restart dtrader-bot dtrader-ws

# Логи
sudo journalctl -u dtrader-bot -f
sudo journalctl -u dtrader-ws -f
```

### Деплой — push на ОБА сервера разом

```bash
cd ~/code/dtrader/dtrader-6

./deploy.sh                  # bot + ws-server на msk и sgp
./deploy.sh bot               # только bot, на оба сервера
./deploy.sh ws                # только ws-server, на оба сервера
./deploy.sh bot msk           # только bot, только на msk
./deploy.sh --config-only     # обновить только config.yaml, без пересборки
```

Скрипт собирает Go-бинарники локально, передаёт со сжатием (`scp -C`),
ретраит до 4 раз при обрыве соединения (актуально для менее стабильного
канала до `msk`). `bot` и `ws-server` деплоятся независимо — падение
одного не блокирует деплой другого.

### bootstrap.sh

Идемпотентная первичная настройка нового VDS с нуля: Go, Redis, UFW,
структура папок, systemd unit-файлы. Безопасно перезапускать повторно
на уже настроенном сервере.

### tunnel.sh (опционально)

SSH-туннели к ws-server (`up`/`down`/`status`) — на случай, если нужен
доступ в обход публичного порта 9000.

---

## 6. REDIS СХЕМА

### Текущие ключи

| Ключ                           | Тип    | TTL | Содержимое                                   |
| ------------------------------ | ------ | --- | -------------------------------------------- |
| `market:trades:{symbol}`       | Stream | —   | тики: price, size, ts (лимит из config.yaml) |
| `market:orderbook:{symbol}`    | String | —   | JSON снапшот стакана                         |
| `market:candles:1m:{symbol}`   | List   | —   | закрытые свечи (лимит из config.yaml)        |
| `market:liquidations:{symbol}` | Stream | —   | ликвидации (лимит из config.yaml)            |
| `market:stats:{symbol}`        | String | —   | JSON: lsr_taker, open_interest_usd           |
| `system:exchange_ping`         | String | 60s | JSON: {"current":X,"ema":Y} RTT биржи        |
| `system:bot_metrics`           | String | 60s | JSON: {"dropped_publications":N} — **новое** |
| `account:balance`              | String | —   | JSON: {"total","margin","leverage"}          |

**`system:bot_metrics`** — новый ключ, добавлен при рефакторинге bot.
Счётчик неудачных попыток публикации в Redis (`Publisher.Metrics`,
`atomic.Int64`), обновляется раз в 10s вместе с ping-лупом. В бою на
обоих VDS сейчас `dropped_publications: 0`.

Лимиты хранения (`market:trades`, `market:candles:1m`,
`market:liquidations`) больше не захардкожены в коде — берутся из
`config.yaml` (`storage.trades`, `storage.candles_1m`,
`storage.liquidations`), см. раздел 8.

### Планируемые ключи (будущие сервисы)

| Ключ                              | Сервис           | Содержимое         |
| --------------------------------- | ---------------- | ------------------ |
| `indicators:ema:{tf}:{symbol}`    | analyzer         | EMA по таймфреймам |
| `indicators:volume:{tf}:{symbol}` | analyzer         | объёмное давление  |
| `signals:entry:{symbol}`          | signal-engine    | сигналы входа      |
| `positions:current`               | position-tracker | открытые позиции   |
| `positions:pnl`                   | position-tracker | P&L реальный       |

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

### bot/ (market-data) — ПОЛНОСТЬЮ ОТРЕФАКТОРЕН

```
bot/
├── cmd/main.go              — точка входа, цикл реконнекта.
│                                Интервалы реконнекта/ping теперь
│                                из cfg.Exchange.*Duration(), не хардкод.
├── config.yaml               — добавлено storage.liquidations
└── internal/
    ├── config/
    │   └── config.go          — ReconnectInterval/PingInterval парсятся
    │                             в time.Duration при Load() (были строки
    │                             без парсинга, TODO так и висел). Валидация
    │                             Orderbook.Depth и Storage.* — падаем на
    │                             старте с понятной ошибкой, если 0 или не
    │                             задано, а не молча теряем данные в рантайме.
    ├── gateway/
    │   ├── protocol.go         — [НОВЫЙ] структуры протокола Gate.io:
    │   │                          WSRequest/WSResponse/WSError, Trade,
    │   │                          OrderBookUpdate, Candle, Liquidation,
    │   │                          ContractStats. Только данные, без логики.
    │   ├── connection.go       — [НОВЫЙ] WSClient (тип), NewWSClient,
    │   │                          Connect/Close, writeJSON/writeMessage.
    │   │                          Явный Dialer{Proxy: nil} — не зависит
    │   │                          от системных HTTP_PROXY/HTTPS_PROXY.
    │   ├── pingloop.go         — [НОВЫЙ] sendPing, RunPingLoop(ctx, interval),
    │   │                          updateEMA, emaAlpha=2/101. Interval теперь
    │   │                          параметр (из config.yaml), не хардкод 10s.
    │   ├── parser.go           — [НОВЫЙ] handleTrades/handleOrderBook/
    │   │                          handleCandles/handleLiquidations/
    │   │                          handleContractStats + parseLiquidations.
    │   │                          Каждый handle* при ошибке публикации:
    │   │                          лог + pub.Metrics.IncDropped().
    │   ├── ws.go               — [СИЛЬНО СОКРАЩЁН, 340→~75 строк] теперь
    │   │                          только ReadLoop — тонкий диспетчер:
    │   │                          читает байты → парсит конверт →
    │   │                          служебные случаи (pong/error/subscribe)
    │   │                          → передаёт в нужный handle* из parser.go.
    │   ├── subscribe.go        — SubscribeOrderBookUpdate(symbols, depth) —
    │   │                          depth теперь параметр из config.yaml,
    │   │                          был хардкод "20".
    │   ├── rest.go             — без изменений логики
    │   ├── client.go           — NewClient: явный Transport{Proxy: nil} —
    │   │                          та же защита от системных прокси, что и
    │   │                          в connection.go (см. ниже "Найденный баг").
    │   └── constants.go        — без изменений
    ├── publisher/
    │   ├── redis.go            — убран мусор (тройной дубль комментария,
    │   │                          мёртвая заглушка PublishExchangePingV2).
    │   │                          maxTrades/maxLiquidations/maxCandles
    │   │                          теперь поля Publisher (из config.yaml
    │   │                          через New(...)), не константы файла.
    │   │                          Новый метод PublishMetrics(ctx).
    │   └── metrics.go          — [НОВЫЙ] Metrics{dropped atomic.Int64},
    │                              IncDropped()/Dropped() — потокобезопасный
    │                              счётчик пропущенных публикаций.
    └── utils/                  — без изменений (hmac.go, http.go, time.go)
```

**Найденный и закрытый баг (сетевой, не логический):** и `http.Client`
(client.go), и `websocket.Dialer` (connection.go) по умолчанию в Go
читают системные переменные окружения `HTTP_PROXY`/`HTTPS_PROXY` через
`http.ProxyFromEnvironment`. Если в шелле разработчика случайно
остаётся такая переменная (например, после экспериментов с VPN-клиентом
типа Hiddify) — бот пытается идти через несуществующий прокси и падает
с `connection refused`/`context deadline exceeded`, хотя сеть и код в
порядке. Закрыто явным `Proxy: nil` в обоих клиентах — теперь бот
всегда ходит к Gate.io напрямую, вне зависимости от окружения, где он
запущен.

**Все правки подтверждены `go build ./...` + `go vet ./...` (чисто) и
живым прогоном на обоих VDS: баланс и позиции читаются, WS-подписки на
все 5 каналов подтверждены, `system:bot_metrics` = `dropped_publications: 0`
на обоих серверах.**

### ws-server/ — без изменений в этом цикле работы

```
ws-server/
├── cmd/main.go              — точка входа ws-server
└── internal/
    ├── config/config.go      — порт 9000, символы, redis
    ├── hub/hub.go             — менеджер WS клиентов (broadcast)
    ├── reader/redis.go        — чтение Redis, агрегация trades 500ms,
    │                            heartbeat 10s, broadcastSystem
    └── handler/ws.go          — WS handler, аутентификация по API ключу
```

---

## 9. СТРУКТУРА ПРОЕКТА dtrader-tui-6

```
dtrader-tui-6/
├── cmd/main.go              — точка входа
├── internal/
│   ├── config/config.go      — .env: WS_SERVER_URL, WS_API_KEY, CRYPTOPANIC_API_KEY
│   ├── news/client.go        — RSS Cointelegraph RU (каждые 5 мин)
│   ├── ws/client.go           — WebSocket клиент с автореконнектом
│   └── ui/
│       ├── app.go             — главная Model (оркестратор bubbletea)
│       ├── styles.go          — ВСЕ стили: orange=214, borders оранжевые
│       ├── header.go          — ⚡ DTrader 6 | время | баланс | PnL | SERV | EXCH | ⚙
│       ├── footer.go          — командная строка
│       ├── layout.go          — renderMain: tabs + [content|rightbar]
│       ├── tabs.go            — powerline вкладки с оранжевыми border
│       ├── news.go            — RSS лента новостей (синий текст)
│       ├── rightbar.go        — стили Logs и Positions
│       ├── sidebar.go         — addLog()
│       ├── settings.go        — иконка ⚙ (заглушка, будет модалка)
│       └── screens/
│           ├── dashboard.go   — 📊 таблица: пара/цена/buy_vol/sell_vol/LSR/OI
│           └── pair.go        — детальный экран пары
├── .env                       — секреты (НЕ в git!)
└── CHECKPOINT.md
```

---

## 10. .ENV ФАЙЛЫ

### На каждом VDS: ~/dtrader-6/shared/config/bot.env

```
GATE_API_KEY=...
GATE_API_SECRET=...
REDIS_PASSWORD=...   # РАЗНЫЙ на msk и на sgp — см. раздел 3
```

⚠️ Сейчас `GATE_API_KEY`/`GATE_API_SECRET` — ОДИН И ТОТ ЖЕ на обоих
серверах (один аккаунт Gate.io, failover-схема). Бот пока read-only
(`GetUnifiedBalance`, `GetPositions`, публичный WS) — реальных ордеров
не создаёт, так что риска дублирующихся сделок сейчас нет. Но когда
появится `executor` — вопрос раздельных ключей (например read-only
ключ для pre-prod/msk, полноценный торговый — только для prod/sgp)
нужно решить осознанно, ДО того как на msk появится код, способный
слать реальные ордера. Сознательно отложено до работы над `executor`.

### На каждом VDS: ~/dtrader-6/shared/config/ws-server.env

```
WS_API_KEY=...
REDIS_PASSWORD=...   # тот же пароль, что в bot.env этого сервера
```

### dtrader-tui-6/.env (локалка)

```
WS_SERVER_URL=ws://<IP-нужного-сервера>:9000/ws
WS_API_KEY=dtrader6_ws_secret
CRYPTOPANIC_API_KEY=79f2be56e48ea3978d8992bcd57791c14554a505
```

---

## 11. ДИЗАЙН-СИСТЕМА TUI

```
Фирменный цвет: оранжевый lipgloss.Color("214")
Все рамки: оранжевые colorBorder="214"
Статус OK: зелёный "82"
Статус WARNING: жёлтый "226"
Статус SOS/OFF: красный "196"
Текст важный: белый "255"
Текст данные: оранжевый "214"
Текст вспомог.: серый "239"
Новости: синий "39"
```

### Header (3 строки с рамкой)

```
╭─────────────────────────────────────────────────────────────────╮
│ ⚡ DTrader 6  09:19 UTC  💰$25.27  ↑+$0.17  ↑+$2.43  ●SERV ●EXCH ⚙│
╰─────────────────────────────────────────────────────────────────╯
```

### Индикаторы

- **SERV**: зелёный <100ms, жёлтый ≥100ms, красный OFF
- **EXCH**: зелёный <300ms, жёлтый 300-1000ms, красный ≥1000ms SOS

### Горячие клавиши

| Клавиша         | Действие                     |
| --------------- | ---------------------------- |
| Tab / Shift+Tab | следующая/предыдущая вкладка |
| Ctrl+1..5       | прямой переход к вкладке     |
| Ctrl+C          | выход                        |

---

## 12. EMA ЛАТЕНТНОСТИ

```
α = 2/(100+1) ≈ 0.0198
EMA = current × α + prev_EMA × (1-α)
Инициализация: первым значением (emaLat == 0 → emaLat = current)
Ping интервал: из config.yaml (exchange.ping_interval), по умолчанию 10s
Redis ключ: system:exchange_ping → {"current": X, "ema": Y}
```

Замеры в бою (2026-07-25):

- `sgp` (prod): current≈70ms, ema≈71ms — стабильно, лучший сервер
- `msk` (pre-prod): current≈234ms, ema≈234ms — хуже sgp, но в 5+ раз
  лучше первичных пессимистичных замеров (~1.14s, до 1.7s); латентность
  может плавать в зависимости от загрузки сети

---

## 13. ПЛАН РЕФАКТОРИНГА

### ✅ Приоритет 1 — market-data (bot) — ЗАВЕРШЁН

Все пункты закрыты, см. раздел 8 (bot/) для деталей:

- `gateway/ws.go` (монолит 340 строк) разбит на protocol/connection/
  pingloop/parser/ws — каждый файл одна ответственность
- Обработка ошибок публикации: лог + счётчик (`publisher/metrics.go`),
  публикуется в Redis раз в 10s (`system:bot_metrics`)
- EMA-логика вынесена в pingloop.go (было прямо в WSClient вперемешку
  с остальным)
- Конфиг оживлён: `ReconnectInterval`/`PingInterval` → `time.Duration`,
  `Orderbook.Depth`, `Storage.*` подключены к реальному использованию,
  добавлена валидация
- Побочно найден и закрыт баг с системными прокси-переменными
  (`Proxy: nil` в обоих HTTP/WS клиентах)
- Подтверждено `go build`/`go vet` + живой прогон на msk и sgp

Не сделано намеренно (отложено до `executor`):

- structured logging (slog) — не критично, `log.Printf` работает,
  можно вернуться при желании
- graceful shutdown publisher (дождаться in-flight записей в Redis
  перед выходом) — для read-only market-data не критично, но стоит
  сделать паттерном, когда появится executor (там цена ошибки другая)
- разделение GATE_API_KEY на read-only (msk) / полный (sgp) — см.
  раздел 10, актуально станет при появлении executor

### Приоритет 2 — ws-server

- `reader/redis.go` — разбить по файлам (trades.go, stats.go, system.go)
- Добавить graceful shutdown
- Улучшить обработку переподключений клиентов

### Приоритет 3 — TUI layout

- Финальное выравнивание (правые borders ±1 символ)
- Сброс buy/sell vol каждую минуту
- Реальный P&L из position-tracker
- Подключить и протестировать живьём против ws-server на msk/sgp
  (готово со стороны сервера, TUI ещё не тестировался живьём)

### 🔶 Приоритет 4 — analyzer (СЛЕДУЮЩИЙ В РАБОТЕ)

Первый сервис, читающий данные из Redis (`market:trades:*`,
`market:candles:1m:*`, `market:orderbook:*`) и считающий индикаторы
для TVP-Sniper:

- T (таймфреймы 1m/8m/24m) — тренд на нескольких ТФ
- V (объёмы) — давление покупок/продаж
- P (давление стакана) — order book imbalance

Результаты пишет в `indicators:*` (см. раздел 6, планируемые ключи).
Начинается в отдельном чате с чистым контекстом.

### Приоритет 5 — остальные новые сервисы

1. signal-engine (TVP-Sniper, читает indicators:_, пишет signals:_)
2. risk-guard
3. executor (тут же — вопрос раздельных API-ключей msk/sgp, см. раздел 10)
4. position-tracker

---

## 14. БЕЗОПАСНОСТЬ (PENDING)

- Закрыть порт 9000 до конкретных IP (сейчас открыт всем — осознанно,
  там только публичные рыночные данные + WS_API_KEY защита)
- REDIS_PASSWORD уже разный на каждом VDS (сделано)
- PostgreSQL (если используется) — доступен только с localhost
- Раздельные GATE_API_KEY для msk/sgp — отложено до executor (раздел 10)
