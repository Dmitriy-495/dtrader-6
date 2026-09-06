#!/usr/bin/env python3
"""
DTrader 6 — Скрипт скачивания исторических свечей с Gate.io Futures.

Скачивает НАТИВНЫЕ 1m-свечи через официальный публичный REST API v4
(https://www.gate.com/docs/developers/apiv4/en/futures/, без API-ключа,
read-only эндпоинт) и агрегирует их в 8m/24m ровно той же логикой, что
использует боевой analyzer (analyzer/internal/reader/candles.go:
Aggregate/mergeCandles) — это намеренно: чтобы бэктест на этих данных
проверял ту же методологию агрегации, что реально крутится на проде,
а не отдельную "похожую".

⚠️ ГЛУБИНА ИСТОРИИ ОГРАНИЧЕНА GATE.IO, НЕ ЭТИМ СКРИПТОМ.
Официальная документация заявляет лимит 2000 точек на один запрос, но
на практике (проверено 2026-09, воспроизведено в issue сторонних
библиотек ccxt/freqtrade/passivbot) Gate.io с 6 февраля 2026 без
анонса ввёл ЕЩЁ ОДИН, недокументированный лимит: запрос отклоняется
с ошибкой "Candlestick too long ago. Maximum 10000 points recently
are allowed", если запрошенное начало диапазона отстоит от текущего
момента больше, чем на 10000 точек ВЫБРАННОГО интервала. Для 1m это
10000 минут ≈ 6.94 суток — то есть глубже примерно недели истории
1m-свечей через этот эндпоинт получить нельзя, сколько бы страниц ни
запрашивать. Скрипт проверяет это на старте и предупреждает, а не
падает посреди долгого скачивания.

Для более глубокой истории пришлось бы либо использовать более крупный
нативный интервал Gate.io (5m/15m/1h/8h/1d — у них тот же потолок в
10000 точек, но каждая точка покрывает больше времени), либо
накапливать 1m-историю постепенно через сам bot проекта (см. решение
автора 2026-09-03: начинаем с ~7 дней точного 1m через этот скрипт,
дальше копим через бот). Этот скрипт сознательно НЕ реализует
переключение на более крупный интервал — агрегация 8m/24m из
НЕ-1m-источника перестала бы быть идентичной боевому analyzer
(см. следующий абзац), и это отдельное архитектурное решение, которое
не стоит принимать молча внутри скрипта скачивания данных.

ВАЖНО — что этот скрипт НЕ делает:
  - Не скачивает историю стакана (order book) — публичного бесплатного
    архива Level 2 для Gate.io Futures в общем доступе нет (см.
    обсуждение в чате: коммерческие сервисы вроде Tardis.dev дают это
    платно, кроме одного дня в месяц). Значит по P (давлению стакана)
    бэктест на этих данных будет НЕВОЗМОЖЕН — только по T и V.
  - Не публикует ничего в Redis и не трогает боевую систему. Это
    отдельный, одноразовый инструмент, результат — CSV-файлы на диске.
  - Не требует API-ключа Gate.io — эндпоинт публичный, read-only.

Использование:
    python3 download_history.py                                   # берёт всё из config.yaml
    python3 download_history.py --symbol BTC_USDT                  # переопределяет symbols из конфига
    python3 download_history.py --symbol BTC_USDT --days 3         # + переопределяет days
    python3 download_history.py --config /path/to/other/config.yaml

Настройки (список символов, глубина истории, папка вывода, частота
запросов) вынесены в config.yaml рядом со скриптом — см. этот файл
для деталей. Любой параметр из командной строки переопределяет
одноимённое значение из config.yaml, сам config.yaml не меняется.

Зависимости: стандартная библиотека Python 3 (urllib, csv, json) +
PyYAML для чтения config.yaml (pip install pyyaml). Если PyYAML не
установлен, скрипт сообщит об этом понятной ошибкой, а не упадёт
трудночитаемым traceback.
"""

import argparse
import csv
import json
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path

try:
    import yaml
except ImportError:
    print(
        "❌ Не найден пакет PyYAML, необходимый для чтения config.yaml.\n"
        "   Установите его командой: pip install pyyaml",
        file=sys.stderr,
    )
    sys.exit(1)

# --- Константы API ------------------------------------------------------

# Публичный REST API v4 Gate.io Futures. settle=usdt — расчёт в USDT,
# совпадает с тем, что использует bot (см. bot/config.yaml: symbols
# вида BTC_USDT — это USDT-margined перпетуалы).
BASE_URL = "https://api.gateio.ws/api/v4/futures/usdt/candlesticks"

# Лимит одного запроса согласно официальной документации Gate.io API v4
# (https://www.gate.com/docs/developers/apiv4/en/futures/ —
# "Maximum of 2000 points are returned in one query"), проверено в
# этой сессии на официальном источнике 2026-09-03.
MAX_CANDLES_PER_REQUEST = 2000

# НЕДОКУМЕНТИРОВАННЫЙ лимит Gate.io (введён без анонса ~2026-02-06,
# подтверждён несколькими независимыми источниками, см. докстринг
# модуля выше): запрос отклоняется, если начало диапазона отстоит от
# текущего момента больше, чем на это число точек ВЫБРАННОГО интервала.
UNDOCUMENTED_MAX_POINTS_FROM_NOW = 10000

# --- Скачивание нативных 1m-свечей --------------------------------------


def max_history_days(interval_seconds: int = 60) -> float:
    """Максимальная глубина истории (в сутках) для заданного интервала
    при недокументированном потолке в UNDOCUMENTED_MAX_POINTS_FROM_NOW
    точек. По умолчанию считает для 1m (60 секунд) — единственный
    интервал, который скачивает этот скрипт.
    """
    return UNDOCUMENTED_MAX_POINTS_FROM_NOW * interval_seconds / 86400


def fetch_candles_page(contract: str, frm: int, to: int) -> list[dict]:
    """Скачивает одну страницу 1m-свечей за диапазон [frm, to] (unix-секунды).

    Формат ответа Gate.io — список ОБЪЕКТОВ (не позиционных списков!)
    с именованными полями t/v/c/h/l/o, подтверждено официальной
    документацией (https://www.gate.com/docs/developers/apiv4/en/futures/,
    раздел "Futures market K-line chart", пример ответа):
    {"t": 1539852480, "v": "97151", "c": "1.032", "h": "1.032",
     "l": "1.032", "o": "1.032", "sum": "3580"}
    Поле "sum" (объём в quote-валюте) здесь не используется — analyzer
    и его аналог в этом скрипте работают с "v" (объём в контрактах).
    """
    params = f"contract={contract}&interval=1m&from={frm}&to={to}"
    url = f"{BASE_URL}?{params}"

    req = urllib.request.Request(url, headers={"Accept": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            raw = json.loads(resp.read().decode("utf-8"))
    except urllib.error.HTTPError as e:
        body = e.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"Gate.io API вернул {e.code} для {url}: {body}") from e
    except urllib.error.URLError as e:
        raise RuntimeError(f"Не удалось подключиться к Gate.io API: {e}") from e

    candles = []
    for item in raw:
        # item — объект с ключами t/v/c/h/l/o (см. докстринг функции выше).
        candles.append(
            {
                "t": int(item["t"]),
                "o": item["o"],
                "c": item["c"],
                "h": item["h"],
                "l": item["l"],
                "v": item["v"],
            }
        )
    return candles


def fetch_full_history(contract: str, days: int, requests_per_second: float) -> list[dict]:
    """Скачивает всю доступную историю 1m-свечей за последние `days` дней,
    постранично, с паузой между запросами для соблюдения rate limit
    (requests_per_second — из config.yaml, см. настройку requests_per_second).

    Если запрошено больше, чем позволяет недокументированный потолок
    Gate.io (~6.94 суток для 1m), запрос всё равно будет отправлен как
    просили — но Gate.io его отклонит на самой старой странице, и это
    будет видно в выводе как ошибка конкретной страницы, а не тихая
    потеря части данных. Проверка в main() предупреждает об этом заранее.
    """
    now = int(time.time())
    start = now - days * 24 * 60 * 60

    all_candles: list[dict] = []
    page_span_seconds = MAX_CANDLES_PER_REQUEST * 60  # минутные свечи

    frm = start
    while frm < now:
        to = min(frm + page_span_seconds, now)
        print(f"  [{contract}] Скачиваю {frm} .. {to} ...", file=sys.stderr)

        page = fetch_candles_page(contract, frm, to)
        all_candles.extend(page)

        frm = to
        time.sleep(1.0 / requests_per_second)

    # Сортируем по времени и убираем дубликаты на границах страниц
    # (Gate.io может вернуть свечу и в конце одной страницы, и в начале
    # следующей, если границы to/from включительны с обеих сторон).
    seen_ts = set()
    deduped = []
    for c in sorted(all_candles, key=lambda x: x["t"]):
        if c["t"] not in seen_ts:
            seen_ts.add(c["t"])
            deduped.append(c)

    return deduped


# --- Агрегация 1m -> 8m/24m, идентичная analyzer/internal/reader/candles.go ---


def parse_candle(raw: dict) -> dict:
    """Аналог parseRawCandle в analyzer: строки -> float64."""
    return {
        "t": raw["t"],
        "open": float(raw["o"]),
        "close": float(raw["c"]),
        "high": float(raw["h"]),
        "low": float(raw["l"]),
        "volume": float(raw["v"]),
    }


def merge_candles(group: list[dict]) -> dict:
    """Точная копия логики mergeCandles из analyzer/internal/reader/candles.go:
    Open группы = Open первой свечи, Close = Close последней,
    High/Low = максимум/минимум по всей группе, Volume = сумма.
    """
    merged = {
        "t": group[0]["t"],
        "open": group[0]["open"],
        "close": group[-1]["close"],
        "high": group[0]["high"],
        "low": group[0]["low"],
        "volume": 0.0,
    }
    for c in group:
        if c["high"] > merged["high"]:
            merged["high"] = c["high"]
        if c["low"] < merged["low"]:
            merged["low"] = c["low"]
        merged["volume"] += c["volume"]
    return merged


def aggregate(one_min: list[dict], minutes: int) -> list[dict]:
    """Точная копия логики Aggregate из analyzer/internal/reader/candles.go:
    группировка с КОНЦА среза (от самых свежих данных назад), чтобы
    "неполный" остаток оказался в начале результата, а не в конце —
    последняя агрегированная свеча всегда полная группа.
    """
    if minutes <= 0 or len(one_min) == 0:
        return []

    n = len(one_min) // minutes
    if n == 0:
        return []

    start = len(one_min) - n * minutes
    result = []
    for i in range(start, len(one_min), minutes):
        group = one_min[i : i + minutes]
        result.append(merge_candles(group))
    return result


# --- CSV-вывод ------------------------------------------------------------


def write_csv(path: Path, candles: list[dict]) -> None:
    with open(path, "w", newline="") as f:
        writer = csv.writer(f)
        writer.writerow(["timestamp", "open", "high", "low", "close", "volume"])
        for c in candles:
            writer.writerow([c["t"], c["open"], c["high"], c["low"], c["close"], c["volume"]])


# --- Основной сценарий ------------------------------------------------------


def process_symbol(symbol: str, days: int, out_dir: Path, requests_per_second: float) -> None:
    print(f"=== {symbol} ===", file=sys.stderr)

    raw_candles = fetch_full_history(symbol, days, requests_per_second)
    print(f"  Скачано {len(raw_candles)} нативных 1m-свечей", file=sys.stderr)

    if not raw_candles:
        print(f"  ⚠️  Нет данных для {symbol}, пропускаю", file=sys.stderr)
        return

    one_min = [parse_candle(c) for c in raw_candles]

    out_dir.mkdir(parents=True, exist_ok=True)
    write_csv(out_dir / f"{symbol}_1m.csv", one_min)

    for minutes, label in [(8, "8m"), (24, "24m")]:
        aggregated = aggregate(one_min, minutes)
        write_csv(out_dir / f"{symbol}_{label}.csv", aggregated)
        print(f"  {label}: {len(aggregated)} свечей -> {out_dir / f'{symbol}_{label}.csv'}", file=sys.stderr)

    print(f"  1m: {len(one_min)} свечей -> {out_dir / f'{symbol}_1m.csv'}", file=sys.stderr)


# --- Конфигурация -----------------------------------------------------


DEFAULT_CONFIG_PATH = Path(__file__).parent / "config.yaml"


def load_config(config_path: Path) -> dict:
    """Читает config.yaml. Отсутствие файла — фатальная, понятная ошибка,
    а не молчаливый откат на зашитые в код значения по умолчанию: если
    пользователь явно указал --config на несуществующий путь, лучше
    остановиться, чем незаметно скачать не то, что он просил.
    """
    if not config_path.exists():
        print(f"❌ Файл конфигурации не найден: {config_path}", file=sys.stderr)
        sys.exit(1)

    with open(config_path, "r", encoding="utf-8") as f:
        config = yaml.safe_load(f)

    if not isinstance(config, dict):
        print(f"❌ {config_path} должен содержать YAML-словарь верхнего уровня", file=sys.stderr)
        sys.exit(1)

    return config


# --- Основной сценарий ------------------------------------------------------


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument(
        "--config",
        type=str,
        default=str(DEFAULT_CONFIG_PATH),
        help=f"Путь к config.yaml (по умолчанию {DEFAULT_CONFIG_PATH}).",
    )
    parser.add_argument(
        "--symbol",
        action="append",
        default=None,
        help="Символ фьючерса Gate.io, напр. BTC_USDT. Можно указать несколько раз. "
        "Переопределяет symbols из config.yaml, если передан хотя бы один раз.",
    )
    parser.add_argument(
        "--days",
        type=int,
        default=None,
        help="Сколько дней истории скачать назад от текущего момента. "
        "Переопределяет days из config.yaml, если передан.",
    )
    parser.add_argument(
        "--out",
        type=str,
        default=None,
        help="Директория для CSV-файлов. Переопределяет out_dir из config.yaml, если передан.",
    )
    args = parser.parse_args()

    config = load_config(Path(args.config))

    # CLI-аргумент переопределяет config.yaml только если он реально был
    # передан (не None) — иначе используем значение из конфига. Так
    # `--symbol BTC_USDT` без `--days` не сбрасывает days на 0 или на
    # какой-то отдельный CLI-дефолт, а честно берёт days из config.yaml.
    symbols = args.symbol if args.symbol is not None else config.get("symbols")
    days = args.days if args.days is not None else config.get("days")
    out_dir = Path(args.out if args.out is not None else config.get("out_dir", "./history"))
    requests_per_second = config.get("requests_per_second", 10)

    if not symbols:
        print(
            "❌ Не указаны символы: передайте --symbol или заполните "
            "symbols в config.yaml",
            file=sys.stderr,
        )
        sys.exit(1)
    if not days:
        print(
            "❌ Не указана глубина истории: передайте --days или заполните "
            "days в config.yaml",
            file=sys.stderr,
        )
        sys.exit(1)

    limit_days = max_history_days()
    if days > limit_days:
        print(
            f"⚠️  Запрошено {days} дней, но Gate.io отдаёт максимум "
            f"~{limit_days:.1f} дней истории для 1m-свечей (недокументированный "
            f"потолок в {UNDOCUMENTED_MAX_POINTS_FROM_NOW} точек от текущего момента, "
            f"см. докстринг модуля). Самые старые страницы будут отклонены "
            f"с ошибкой 'Candlestick too long ago' — это не баг скрипта.",
            file=sys.stderr,
        )

    for symbol in symbols:
        try:
            process_symbol(symbol, days, out_dir, requests_per_second)
        except RuntimeError as e:
            print(f"❌ Ошибка при обработке {symbol}: {e}", file=sys.stderr)
            print(f"   Пропускаю {symbol}, продолжаю с остальными символами.", file=sys.stderr)
            continue

    print("Готово.", file=sys.stderr)


if __name__ == "__main__":
    main()
