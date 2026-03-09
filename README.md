# DTrader 6

Multi-timeframe crypto trading bot for Gate.io

## Architecture

- `bot`       — Gate.io WebSocket → Redis
- `ws-server` — Redis → WebSocket clients
- `ws-client` — temporary data viewer (Node.js)

## Stack

- Go 1.22.3
- Redis 6
- Node.js 24
- Gate.io Futures WebSocket v4

## Timeframes

- HTF 24m — trend & permission
- MTF  8m — setup & volume
- LTF  1m — sniper entry
