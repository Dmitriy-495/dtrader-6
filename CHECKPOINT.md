# DTrader 6 — Полный чекпоинт системы (2026-08-08)

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

| ID  | Имя              | Бывшее имя   | Статус                                  | Роль                                 |
| --- | ---------------- | ------------ | --------------------------------------- | ------------------------------------ |
| A   | market-data      | bot          | ✅ Задеплоен msk+sgp (orderbook snapshot — см. 13b) | Gate.io → Redis           |
| B   | executor         | trader       | ⬜ Планируется                          | Сигналы → Ордера Gate.io             |
| C   | signal-engine    | strategies   | ⬜ Планируется                          | Индикаторы → Сигналы TVP-Sniper      |
| D   | analyzer         | indicators   | ✅ Задеплоен msk+sgp (см. раздел 13a, 13c)   | Поток данных → Индикаторы       |
| E   | risk-guard       | risk-manager | ⬜ Планируется                          | Фильтрация сигналов, защита капитала |
| F   | ws-server        | ws-server    | ✅ Работает                             | Redis → WebSocket → TUI              |
| G   | position-tracker | —            | ⬜ Планируется                          | Позиции, P&L реальный                |
| Z   | Redis            | Redis        | ✅ Работает (на каждом VDS)             | Шина данных                          |

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
github.com/Dmitriy-495/dtrader-6      ветка master (bot + ws-server + analyzer)
github.com/Dmitriy-495/dtrader-tui-6  ветка main   (TUI, ПУБЛИЧНЫЙ)
```

`analyzer/` — новый сервис в корне репозитория `dtrader-6`, рядом с
`bot/` и `ws-server/` (свой `go.mod`, `github.com/Dmitriy-495/dtrader-6/analyzer`,
те же версии зависимостей `go-redis`/`godotenv`/`yaml`, что и в остальных
сервисах — единообразие сознательное, не дублирование версий без
причины). Добавлен и запушен 2026-08-02 (см. раздел 13a).

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

./deploy.sh                  # bot + ws-server + analyzer на msk и sgp
./deploy.sh bot               # только bot, на оба сервера
./deploy.sh ws                # только ws-server, на оба сервера
./deploy.sh analyzer          # только analyzer, на оба сервера
./deploy.sh bot msk           # только bot, только на msk
./deploy.sh --config-only     # обновить config.yaml всех трёх сервисов, без пересборки
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

### ⬜ TODO: bot.log/bot.error.log перепутаны (найдено 2026-08-08, см. 13c)

Весь код `bot` использует стандартный `log.Printf`, который в Go
всегда пишет в stderr независимо от смысла сообщения — значит
`StandardError=append:.../bot.error.log` в systemd unit получает ВСЁ
подряд, включая обычные информационные строки (`🕯️ свеча записана`,
`📖 подписка отправлена`), а не только реальные ошибки. Затрудняет
диагностику через `grep` по `.error.log` (см. как это сбило с толку
диагностику orderbook-проблемы на sgp в разделе 13c). Похоже, это та
самая незавершённая `bot/internal/logging/` работа, упомянутая как
"не в этом коммите, другая задача" в эстафете 13b. Не критично
функционально, но кандидат на отдельную небольшую задачу: разделить
`log.Printf` на два логгера (info → stdout, error/warn → stderr) или
использовать префиксы + единый вывод с последующей фильтрацией.

### ✅ analyzer в deploy.sh/bootstrap.sh — готово 2026-08-08

`bootstrap.sh` — `dtrader-analyzer.service` добавлен (уже было готово
на момент передачи эстафеты 13b): `Restart=on-failure`,
`After=... dtrader-bot.service` БЕЗ `Requires=dtrader-bot.service` —
осознанное решение: analyzer не должен падать/блокироваться, если bot
временно недоступен или перезапускается, он просто читает
пустые/устаревшие `market:*` ключи, пока bot не восстановится. Папка
`bin/analyzer` создаётся в общей структуре. Финальный чеклист
дополнен пунктом про `analyzer.env` (нужен только `REDIS_PASSWORD`).

`deploy.sh` — добавлена полная поддержка `analyzer` как цели
деплоя: `./deploy.sh analyzer` (только analyzer), `./deploy.sh`
без аргументов теперь деплоит **все три** сервиса (bot+ws+analyzer),
`./deploy.sh --config-only` тоже обновляет все три config.yaml и
рестартует все три сервиса. Сборка — теми же флагами кросс-компиляции,
что у bot/ws-server (`GOOS=linux GOARCH=amd64 CGO_ENABLED=0`,
`-ldflags="-s -w"`), health-check после рестарта (`systemctl
is-active`, при неудаче — последние 15 строк `journalctl`).

**Проверено:** синтаксис обоих скриптов (`bash -n`), диспетчеризация
`TARGET` во всех вариантах (`all`/`bot`/`ws`/`analyzer`/
`--config-only`/невалидный таргет — каждый выставляет верные флаги),
пути `bin/analyzer/...` совпадают между bootstrap.sh и deploy.sh,
кросс-компиляция analyzer с точными deploy-флагами даёт статический
бинарник (5.4MB, `CGO_ENABLED=0`, без внешних зависимостей),
живой прогон этого бинарника против Redis — все 3 символа
(BTC/ETH/SOL) стартуют каждый в своей горутине, чистый shutdown.

**Не проверено** (требует реального SSH-доступа к msk/sgp, недоступно
в песочнице): фактический деплой по сети, поведение `scp_retry` на
нестабильном канале, systemd behaviour на реальном VDS.

Следующий шаг — фактический прогон `./deploy.sh analyzer msk` с
локальной машины автора (см. раздел 13a, живой прогон на реальном
Gate.io ещё предстоит сделать именно там, не в песочнице).

---

## 6. REDIS СХЕМА

### Текущие ключи

| Ключ                           | Тип    | TTL | Содержимое                                                                                                  |
| ------------------------------ | ------ | --- | ----------------------------------------------------------------------------------------------------------- |
| `market:trades:{symbol}`       | Stream | —   | тики: price, size, ts (лимит из config.yaml)                                                                |
| `market:orderbook:{symbol}`    | String | —   | ✅ Полный, поддерживаемый стакан (b/a — все уровни на глубину `orderbook.depth`), не дельта. См. раздел 13b |
| `market:candles:1m:{symbol}`   | List   | —   | закрытые свечи (лимит из config.yaml)                                                                       |
| `market:liquidations:{symbol}` | Stream | —   | ликвидации (лимит из config.yaml)                                                                           |
| `market:stats:{symbol}`        | String | —   | JSON: lsr_taker, open_interest_usd                                                                          |
| `system:exchange_ping`         | String | 60s | JSON: {"current":X,"ema":Y} RTT биржи                                                                       |
| `system:bot_metrics`           | String | 60s | JSON: {"dropped_publications":N} — **новое**                                                                |
| `account:balance`              | String | —   | JSON: {"total","margin","leverage"}                                                                         |

**`system:bot_metrics`** — новый ключ, добавлен при рефакторинге bot.
Счётчик неудачных попыток публикации в Redis (`Publisher.Metrics`,
`atomic.Int64`), обновляется раз в 10s вместе с ping-лупом. В бою на
обоих VDS сейчас `dropped_publications: 0`.

Лимиты хранения (`market:trades`, `market:candles:1m`,
`market:liquidations`) больше не захардкожены в коде — берутся из
`config.yaml` (`storage.trades`, `storage.candles_1m`,
`storage.liquidations`), см. раздел 8.

### Ключи analyzer — ГОТОВЫ (см. раздел 13a)

| Ключ                              | TTL | Содержимое                                                                             |
| --------------------------------- | --- | -------------------------------------------------------------------------------------- |
| `indicators:trend:{tf}:{symbol}`  | 60s | JSON: {ema_fast, ema_slow, direction, angle, rsi, macd_histogram, ts} — tf ∈ 1m/8m/24m |
| `indicators:volume:{tf}:{symbol}` | 60s | JSON: {buy_vol, sell_vol, delta, spike, ts} — tf ∈ 1m/8m/24m                           |
| `indicators:pressure:{symbol}`    | 60s | JSON: {bid_vol, ask_vol, imbalance, ts} — без {tf}, P не привязан к таймфрейму         |

### Планируемые ключи (будущие сервисы)

| Ключ                     | Сервис           | Содержимое       |
| ------------------------ | ---------------- | ---------------- |
| `signals:entry:{symbol}` | signal-engine    | сигналы входа    |
| `positions:current`      | position-tracker | открытые позиции |
| `positions:pnl`          | position-tracker | P&L реальный     |

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

### analyzer/ — НОВЫЙ сервис, полная структура и детали в разделе 13a

```
analyzer/
├── cmd/main.go
├── config.yaml
└── internal/
    ├── config/config.go
    ├── redisclient/client.go
    ├── reader/{candles,trades,orderbook}.go
    ├── indicator/{ema,rsi,macd,trendangle,trend,volume,pressure}.go
    ├── engine/symbol_engine.go
    └── publisher/redis.go
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

### ✅ Приоритет 4 — analyzer — ГОТОВ (раздел 13a)

### 13a. analyzer — детали реализации

Первый сервис, читающий данные из Redis и считающий индикаторы для
TVP-Sniper. Полностью написан, собран и проверен живым прогоном
(config → engine → reader → indicator → publisher → Redis).

**Таймфреймы:** 1m (нативный, приходит из bot) / 8m / 24m — 8m и 24m
analyzer строит САМ через OHLCV rollup из `market:candles:1m`, bot их
не публикует и не должен (сознательное решение — см. обсуждение ниже).

**Источники и способ чтения:**

| Источник                     | Тип    | Способ чтения                                        |
| ---------------------------- | ------ | ---------------------------------------------------- |
| `market:candles:1m:{symbol}` | List   | poll (раз в calc_interval)                           |
| `market:trades:{symbol}`     | Stream | XREAD блокирующий (нужен каждый тик и порядок для V) |
| `market:orderbook:{symbol}`  | String | poll раз в 1s (снапшот состояния)                    |

**Индикаторы (T/V/P), стартовые параметры — см. `analyzer/config.yaml`:**

- T на 24m: EMA(72)/EMA(50), Trend Angle (линейная регрессия), RSI(14)
- T на 8m: EMA(24)/EMA(12), MACD(12,26,9)
- T на 1m: EMA(21)/EMA(9)
- V: buy/sell объём из потока trades, детект Volume Spike (×2.5 от SMA(20))
- P: `Buy_Pressure = Σbid_vol(20 уровней) / Σask_vol(20 уровней)`

Все цифры взяты как стартовая точка из документа `TVP_SNIPER.md`
(роли HTF/MTF из прошлых обсуждений стратегии) — требуют тестирования
на реальной истории, не финальные боевые значения.

**Важное архитектурное решение:** analyzer публикует ТОЛЬКО сырые
индикаторы T/V/P по отдельности в `indicators:*` (см. раздел 6).
Сборку в единый TVP-сигнал (веса, пороги входа) делает `signal-engine`
— не analyzer. Явно согласованная граница ответственности сервисов.

**Concurrency:** один `Engine` на символ (горутина, независимый цикл),
внутри — 3 читателя (candles/trades/orderbook, независимые) + 1
`calcTicker` (раз в `calc_interval`, по умолчанию 5s, считает T/V/P из
накопленного состояния и публикует). Общее состояние символа защищено
одним `sync.Mutex`. По аналогии с паттерном `TradeAgg` в
`ws-server/internal/reader/redis.go`, расширенным на три источника.

**Структура:**

```
analyzer/
├── cmd/main.go              — точка входа, graceful shutdown (SIGINT/SIGTERM),
│                                Engine на каждый символ в своей горутине
├── config.yaml               — символы, таймфреймы, периоды индикаторов
└── internal/
    ├── config/config.go      — Load() + validate(), тот же паттерн, что в bot
    ├── redisclient/client.go — одно соединение на reader+publisher
    ├── reader/
    │   ├── candles.go         — FetchRecent1m, Aggregate (OHLCV rollup 1m→8m/24m)
    │   ├── trades.go          — XREAD market:trades, тот же паттерн что ws-server
    │   └── orderbook.go       — poll market:orderbook (полный снапшот, см. раздел 13b)
    ├── indicator/              — ЧИСТАЯ математика, никакого Redis/JSON
    │   ├── ema.go, rsi.go, macd.go, trendangle.go
    │   ├── trend.go            — сборка T (EMA+RSI+MACD+Angle) в TrendSnapshot
    │   ├── volume.go           — V: buy/sell/delta/spike
    │   └── pressure.go         — P: bid_vol/ask_vol/imbalance
    ├── engine/symbol_engine.go — Engine на символ, склеивает reader+indicator+publisher
    └── publisher/redis.go      — PublishTrend/PublishVolume/PublishPressure, TTL 60s
```

**✅ Ограничение снято (см. раздел 13b, закрыт 2026-08-07):** `reader/orderbook.go`
был написан под ЦЕЛЕВОЙ формат `market:orderbook:{symbol}` — полный
снапшот стакана (не дельту) — заранее. bot теперь публикует именно
такой полный снапшот по тому же ключу и с теми же именами полей
(`s`/`b`/`a`, `p`/`s` внутри уровня) — как и предполагалось, изменений
в analyzer НЕ потребовалось. P можно доверять на живых данных.

**Живой прогон, подтверждающий работоспособность (2026-08-02):**
поднят локальный Redis, залиты тестовые данные точно в форматах bot
(200 минутных свечей с трендом, 30 живых trades через XADD во время
работы analyzer, полный снапшот orderbook на 20 уровней), собран и
запущен реальный бинарник. Результат в `indicators:*`:

- T на 8m/24m корректно определил `direction: "up"` (данные были с
  восходящим трендом)
- P посчитал `imbalance: 1.51` (bids были специально сделаны жирнее)
- V корректно накопил `buy_vol`/`sell_vol` из живого потока (не из
  истории — `TradeReader` намеренно слушает только НОВЫЕ записи через
  `XREAD ... "$"`, так же как `ws-server`)
- Graceful shutdown по сигналу — чисто, без паник

`go build ./...` и `go vet ./...` — чисто, без единого замечания.

**Не сделано намеренно (отложено):**

- Ликвидации (`market:liquidations`) — не нужны для v1 TVP-Sniper
  (формула T+V+P их не использует), возможный кандидат для будущей
  версии либо сразу для risk-guard (каскады ликвидаций как триггер
  аварийного стопа)
- Поглощение крупных ордеров в P (требует истории стакана во времени,
  не только снапшота) — отложено на следующую итерацию после v1
- LLM-модуль "Стратег" (Deep Seek для доп. тех.анализа, Grok для
  новостного фона) — обсуждалась идея отдельно, осознанно отложена до
  стабилизации базового T/V/P-ядра

### ✅ Приоритет 4.5 — доработка bot: orderbook snapshot — ГОТОВО (раздел 13b)

### 13b. bot — доработка: полный снапшот стакана вместо дельты — ЗАКРЫТО 2026-08-07

**Коммит:** `e1dfad6` — "bot: полный снапшот стакана вместо инкрементальной
дельты (раздел 13b)" — запушен в `master`.

**Проблема (была):** `market:orderbook:{symbol}` содержал последнюю
ИНКРЕМЕНТАЛЬНУЮ дельту стакана (`order_book_update` от Gate.io — это
incremental channel по протоколу биржи), а не полный, поддерживаемый
стакан. Обнаружено при проектировании P-индикатора в analyzer — для
`Buy_Pressure = Σbid_vol/Σask_vol` нужен актуальный полный стакан на
N уровней, не последний присланный кусок изменений.

**Согласованное архитектурное решение:** поддержание полного стакана
(REST-снапшот как база + применение входящих дельт + отслеживание
разрывов последовательности `U`/`u`) — это STATEFUL-логика ПРОТОКОЛА
БИРЖИ, а не логика анализа рынка. Место для неё — `bot`, рядом с
WS-клиентом Gate.io, а НЕ analyzer, по тем же причинам, по которым
`bot` уже владеет единственной точкой интеграции с биржей:

- analyzer не должен обзаводиться собственным REST-клиентом к Gate.io
  просто ради одного вызова снапшота при старте/ресинке
- отслеживание разрывов `U`/`u` требует явного управления самим WS-
  соединением (переподписка при разрыве) — это зона `wsClient.Connect`,
  которым управляет bot, не analyzer
- `market:*` в Redis должен быть самодостаточным для ЛЮБОГО будущего
  потребителя (не только analyzer) — если стакан "доделывает" один
  consumer, второй future-consumer вынужден писать ту же логику заново

**Что сделано в bot (`bot/internal/gateway/`):**

1. `rest.go` — `GetOrderBookSnapshot(ctx, symbol, depth)`: `GET
/futures/usdt/order_book?contract={symbol}&limit={depth}&with_id=true`
   (глубина = `cfg.Orderbook.Depth` из config.yaml, та же, что и в
   подписке на дельты — обязательное требование протокола Gate.io).
   `OrderBookSnapshot`/`OBLevelREST` — размечены `json.Number` для
   `size` (в REST-ответе это JSON-число, в WS — строка; расхождение
   подтверждено прямой проверкой реального ответа биржи).
2. `orderbook.go` (новый файл) — `LocalOrderBook`: держит стакан на
   символ в памяти (`map[float64]bookLevel` для bids/asks — O(1)
   обновление/удаление по цене). Реализует официальный алгоритм
   Gate.io: `newLocalOrderBook` из REST-снапшота (`synced=false`),
   `ApplyDelta` ищет точку стыковки (`U <= lastUpdateID+1 <= u`),
   дальше требует `FirstU == lastUpdateID+1` на каждой следующей
   дельте — иначе `needResync=true`. Отдельно обработан редкий случай
   `Full=true` (биржа сама шлёt принудительный полный снапшот через
   тот же канал) — стакан заменяется целиком. `Snapshot()` сортирует
   bids по убыванию / asks по возрастанию цены.
3. `connection.go` — `WSClient` получил `restClient *Client`, `books
map[string]*LocalOrderBook` + `booksMu sync.Mutex`.
4. `parser.go` — `handleOrderBook` переписан: применяет входящую
   дельту к `LocalOrderBook`, публикует уже ПОЛНЫЙ стакан после
   применения, при разрыве последовательности запускает
   `resyncOrderBook` в отдельной горутине (не блокирует `ReadLoop` на
   время REST-запроса).
5. `main.go` — `InitOrderBookSnapshots` вызывается на КАЖДЫЙ
   `subscribeAll` (в т.ч. при реконнекте — старое состояние
   `LocalOrderBook` не годится для нового WS-соединения, там своя
   нумерация `U`/`u`), строго ДО подписки на `order_book_update`.
6. `publisher.PublishOrderBook` публикует уже ПОЛНЫЙ стакан тем же
   ключом `market:orderbook:{symbol}` и тем же форматом полей
   (`s`/`b`/`a`, `p`/`s` внутри уровня) — analyzer не потребовал
   изменений (см. обновлённый комментарий в
   `analyzer/internal/reader/orderbook.go`).

**Проверено в build-песочнице (2026-08-07, см. SANDBOX_SETUP.md):**

- `go build ./...` и `go vet ./...` на `bot/` — чисто, без замечаний
  (Go 1.22.2 через apt, `GOTOOLCHAIN=local`, `replace` на локальные
  клоны `go.yaml.in/yaml/v3` и `go.uber.org/atomic` — vanity-пути не
  в allowlist песочницы).
- Юнит-тесты на `LocalOrderBook` (5 сценариев): инициализация из
  снапшота, точка стыковки дельты, `size:"0"` → удаление уровня,
  разрыв последовательности → `needResync`, `Full=true` → полная
  замена, сортировка bids/asks в `Snapshot()`. Все прошли с первого
  раза.
- Живой прогон: поднят локальный Redis, реальный бинарник
  опубликовал `OrderBookFullSnapshot` через `publisher.PublishOrderBook`,
  прочитан обратно ключ `market:orderbook:BTC_USDT` — формат
  байт-в-байт совпал с тем, что ожидает `analyzer/internal/reader/orderbook.go`
  (поля `t`/`s`/`b`/`a`, внутри уровня `p`/`s`).

**Не проверено в песочнице (требует VDS — см. SANDBOX_SETUP.md,
ограничение в конце):** реальное сетевое взаимодействие с Gate.io
(живые WS-дельты `order_book_update`, реальные разрывы
последовательности `U`/`u` на боевом потоке). До выката на msk/sgp —
живой прогон против реальной биржи остаётся на стороне Дмитрия.

**Дальше:** можно катить `analyzer` на VDS (см. раздел 5, "✅ analyzer
в deploy.sh/bootstrap.sh — готово 2026-08-08") — блокер снят, P будет
считаться на корректных данных.

### 13b-1. Найден и исправлен баг: параллельные resync на один символ (2026-08-08)

При код-ревью реализации 13b (до передачи дальше по эстафете)
обнаружена реальная логическая проблема, не отловленная в build-
песочнице предыдущей сессии (там честно отмечено "не проверено на
реальных разрывах последовательности под нагрузкой" — и вот что там
оказалось не так):

**Проблема:** `handleOrderBook` при обнаружении разрыва
последовательности запускал `resyncOrderBook` в отдельной горутине
(`go c.resyncOrderBook(...)`) без всякой защиты от повторного
запуска. Поскольку REST-запрос снапшота занимает сотни мс, а
`ReadLoop` — последовательный цикл, продолжающий получать и
обрабатывать новые входящие дельты для ТОГО ЖЕ символа — каждая такая
дельта, применяясь к ещё не обновлённому `c.books[symbol]`, СНОВА
обнаруживала несостыковку `lastUpdateID` и запускала ЕЩЁ ОДИН
параллельный `resyncOrderBook`. Итог: при нестабильной сети — до
нескольких лишних одновременных REST-запросов на один символ, плюс
гонка по порядку завершения (не гарантировано, что "победит" именно
самый свежий из параллельных снапшотов при записи в `c.books[symbol]`).

**Исправление:** добавлено поле `resyncing map[string]bool` в
`WSClient` (защищено тем же `booksMu`, что и `books`). В
`handleOrderBook` — проверка-и-установка флага атомарно под локом
перед запуском горутины; если resync для символа уже идёт — просто
`return`, новая горутина не запускается. В `resyncOrderBook` — сброс
флага гарантирован через `defer`, независимо от успеха/ошибки REST-
запроса, чтобы символ не остался навсегда заблокированным.

**Файлы:** `bot/internal/gateway/connection.go` (поле + инициализация),
`bot/internal/gateway/parser.go` (guard перед запуском горутины),
`bot/internal/gateway/orderbook.go` (defer сброса флага в
`resyncOrderBook`).

**Проверено:** добавлены 2 новых юнит-теста в
`orderbook_test.go` — `TestResyncGuard_PreventsParallelResyncForSameSymbol`
(5 одновременных горутин пытаются пометить один символ, ровно одна
получает право на resync) и `TestResyncOrderBook_ClearsFlagOnFailure`
(флаг гарантированно снимается даже когда `restClient == nil`). Все
8 тестов пакета (6 старых + 2 новых) проходят, включая прогон с
`go test -race` — гонок по памяти нет. `go build ./...` и
`go vet ./...` — чисто на bot и analyzer после правки.

**Дополнительно — сквозная интеграционная проверка (не было в
предыдущей сессии):** опубликован тестовый `OrderBookFullSnapshot` в
точном формате bot, поднят и запущен реальный бинарник `analyzer` —
`indicators:pressure:BTC_USDT` посчитался с правильной арифметикой
(`bid_vol=4.3, ask_vol=2.1, imbalance=4.3/2.1=2.0476` — сошлось
вручную). Подтверждает, что весь путь bot→Redis→analyzer работает
на реальном коде обеих сторон, не только "по документации".

### Приоритет 5 — остальные новые сервисы

1. signal-engine (TVP-Sniper, читает indicators:_, пишет signals:_)
2. risk-guard
3. executor (тут же — вопрос раздельных API-ключей msk/sgp, см. раздел 10)
4. position-tracker

### 13c. Реальный деплой analyzer + bot(13b) на msk и sgp — ЗАВЕРШЁН 2026-08-08

Первый живой прогон всей цепочки market-data → analyzer на боевом
Gate.io, на обоих VDS. Пройдено вручную, командами через SSH — итоги
и обнаруженное по пути зафиксированы здесь для следующей сессии.

**msk (pre-prod) — пройдено первым:**

1. `mkdir` папки `bin/analyzer` вручную → `./deploy.sh analyzer msk` →
   ручной прогон бинарника (без systemd) для первой проверки на
   боевых данных.
2. Проверена персистентность Redis (`save: 900 1 300 10 60 10000`,
   `appendonly: no`) — дефолтный конфиг, RDB работает, `rdb_last_save_time`
   отставал от текущего момента на ~1 минуту на момент проверки.
   Признано безопасным для рестарта.
3. Полный `bootstrap.sh` — создал `dtrader-analyzer.service`,
   перезаписал (без изменений по содержимому) unit-файлы bot/ws,
   рестартовал `redis-server` (~секунда простоя, известный компромисс
   персистентности, принят осознанно).
4. `systemctl enable + start dtrader-analyzer` — сервис поднялся,
   `indicators:trend`/`indicators:pressure` считаются на реальном
   BTC_USDT/ETH_USDT/SOL_USDT с Gate.io.
5. Обнаружено: `dtrader-bot`/`dtrader-ws` были `disabled` (не в
   автозапуске) — исправлено, `systemctl enable dtrader-bot dtrader-ws`.
6. `DBSIZE` и `indicators:pressure` подтвердили, что данные пережили
   рестарт Redis без проблем.

**sgp (prod) — по той же схеме, с находкой:**

Шаги 1-6 повторены аналогично msk. Persistence на sgp — идентична
msk (тот же дефолтный конфиг). При первой проверке `indicators:pressure`
показал аномальные значения (`imbalance: 71.78`, следующий замер
`0.17`), а `market:orderbook:BTC_USDT` содержал **пустой `asks`** и
**зависший уровень с `size:"0"`**, не удалённый из стакана.

**Диагностика (важный урок на будущее):** первая гипотеза (баг в
`ApplyDelta`/`removeLevel` — строковое сравнение `size == "0"` не
ловит decimal-варианты нуля вроде `"0.0"`) была правдоподобной, но
**неверной** — проверка кода показала, что `removeLevel` использует
`price` (не `size`) как ключ map, само сравнение `size == "0"`
работает корректно для строгого протокола Gate.io. Прямой запрос к
REST API Gate.io (`GET /futures/usdt/order_book?with_id=true`) живьём
подтвердил нормальный, полный ответ — значит проблема была не в
самом REST-эндпоинте и не в парсинге.

**Настоящая причина**, найденная по логам bot на sgp: **на sgp всё
это время работала СТАРАЯ версия bot, без доработки 13b вообще.**
`grep "📖 [orderbook] снапшот" bot.error.log` не находил ни одной
строки за 10 дней логов (29 июля — 8 августа) — только старые
`📖 [order_book_update] подписка отправлена` (сообщение о подписке
на канал, существовавшее и до 13b) и обычные WS-реконнекты. `./deploy.sh
analyzer sgp`, который мы выполняли, деплоит ТОЛЬКО analyzer — bot
на sgp никогда не передеплоивался в этом цикле работы, старый
бинарник продолжал публиковать в `market:orderbook` инкрементальную
дельту (та самая исходная проблема раздела 13b), и analyzer читал
её как будто это полный снапшот.

**Побочная находка при диагностике:** `bot.error.log` на sgp
захламлён обычными информационными сообщениями (`🕯️ свеча записана`,
`📖 подписка отправлена`) вперемешку с тем, что должно быть в
`bot.log` — потому что весь код использует стандартный `log.Printf`,
который в Go всегда пишет в stderr независимо от смысла сообщения,
а `StandardOutput`/`StandardError` в systemd unit разделяют потоки
только на уровне ОС, не зная, что сообщение внутри было "успехом", а
не "ошибкой". Это, по всей видимости, та самая недоделанная
`bot/internal/logging/` работа, упомянутая как "не в этом коммите" в
эстафете 13b (см. выше) — не мешает функционально, но затрудняет
диагностику через grep по `.error.log`. Не исправлено в этой сессии
(не относится к analyzer/13b впрямую), кандидат на отдельную задачу.

**Исправление:** `./deploy.sh bot sgp` — актуальный bot (c 13b и с
фиксом resync-гонки, см. 13b-1) собран и задеплоен на sgp. После
рестарта в логе сразу появились ожидаемые `📖 [orderbook] снапшот
получен: {symbol} id=... bids=20 asks=20` на все три символа, следом
одна `🔄 обнаружен разрыв последовательности` на каждый (ожидаемо —
зазор между REST id и первой WS-дельтой) и мгновенная успешная
`пересинхронизация выполнена` — больше повторных разрывов не
зафиксировано за 5+ минут наблюдения. `market:orderbook:BTC_USDT`
стал полным (20/20 уровней), `imbalance` начал давать разные,
живые значения (`3.40` → `0.074` за 60 секунд) — это НЕ баг, а
реальная волатильность давления в стакане на активной паре;
разница почти в 46 раз за минуту — ожидаемое поведение метрики,
которая для того и создавалась, чтобы ловить именно такие резкие
смещения.

**Итоговое состояние на обоих VDS (msk + sgp):**
- `dtrader-bot`, `dtrader-ws`, `dtrader-analyzer` — все `active
  (running)` и `enabled` (автозапуск при перезагрузке)
- `market:orderbook:{symbol}` — полный снапшот на обоих серверах
- `indicators:trend/volume/pressure:*` — считаются на реальных
  данных Gate.io на обоих серверах

**Урок для будущих деплоев:** `./deploy.sh analyzer <host>` деплоит
ТОЛЬКО analyzer — если на целевом хосте есть отставание bot (не
задеплоенные более ранние правки), analyzer будет молча работать на
устаревших/некорректных входных данных без единой ошибки в своих
логах (сам analyzer не может знать, что bot отстал по версии — он
просто читает то, что реально лежит в Redis). Перед деплоем analyzer
на новый хост стоит явно сверять, что bot на нём тоже актуален
(например `md5sum` бинарника или дата последнего `deploy.sh bot
<host>`), а не полагаться на то, что "раз ключи в Redis существуют,
формат правильный".

---

## 14. БЕЗОПАСНОСТЬ (PENDING)

- Закрыть порт 9000 до конкретных IP (сейчас открыт всем — осознанно,
  там только публичные рыночные данные + WS_API_KEY защита)
- REDIS_PASSWORD уже разный на каждом VDS (сделано)
- PostgreSQL (если используется) — доступен только с localhost
- Раздельные GATE_API_KEY для msk/sgp — отложено до executor (раздел 10)
