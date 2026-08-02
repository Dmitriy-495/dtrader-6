// Пакет redisclient создаёт единственное соединение с Redis, которое
// разделяют reader/ (читает market:*) и publisher/ (пишет indicators:*).
//
// Почему не два отдельных клиента (как могло бы показаться логичным по
// аналогии с bot=publisher-only, ws-server=reader-only)? Analyzer — это
// ПЕРВЫЙ сервис в проекте, который одновременно и читает, и пишет в один
// и тот же Redis, поэтому имеет смысл одно соединение с пулом (go-redis
// уже сам управляет пулом соединений внутри), а не два независимых пула
// к одному и тому же серверу.
package redisclient

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// New создаёт клиент Redis с указанными параметрами подключения.
func New(host string, port int, password string, db int) *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", host, port),
		Password: password,
		DB:       db,
	})
}

// Ping проверяет доступность Redis — вызывается один раз при старте
// main(), по тому же принципу, что и pub.Ping(ctx) в bot/cmd/main.go:
// лучше упасть сразу с понятной ошибкой "Redis недоступен", чем
// обнаружить проблему на первом реальном XREAD/SET в рантайме.
func Ping(ctx context.Context, rdb *redis.Client) error {
	return rdb.Ping(ctx).Err()
}
