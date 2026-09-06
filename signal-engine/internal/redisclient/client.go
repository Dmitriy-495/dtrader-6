// Пакет redisclient создаёт единственное соединение с Redis, которое
// разделяют reader/ (читает indicators:*) и publisher/ (пишет signals:*).
// По аналогии с analyzer/internal/redisclient — signal-engine тоже и
// читает, и пишет в один и тот же Redis, поэтому одно соединение с
// пулом (go-redis уже сам управляет пулом соединений внутри).
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
// main(), тот же принцип, что и в analyzer/cmd/main.go: лучше упасть
// сразу с понятной ошибкой "Redis недоступен", чем обнаружить проблему
// на первом реальном Get/Set в рантайме.
func Ping(ctx context.Context, rdb *redis.Client) error {
	return rdb.Ping(ctx).Err()
}
