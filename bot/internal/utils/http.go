// Этот файл содержит вспомогательные функции для HTTP запросов к Gate.io.
// Основная задача — формирование заголовков авторизации для приватных endpoints.
package utils

import (
	"net/http"
	"strconv"
)

// AuthHeaders добавляет заголовки авторизации Gate.io к HTTP запросу.
//
// Gate.io требует для приватных endpoints:
//   KEY       : API ключ
//   SIGN      : HMAC-SHA512 подпись запроса
//   Timestamp : время запроса в секундах Unix
//
// Вызывается ПОСЛЕ setCommonHeaders в client.go —
// не дублирует Content-Type, Accept, User-Agent.
//
// Параметры:
//   - req    : указатель на запрос — модифицируем оригинал, не копию
//   - apiKey : cfg.Secrets.APIKey
//   - secret : cfg.Secrets.APISecret
//   - body   : тело запроса ("" для GET, JSON для POST)
func AuthHeaders(req *http.Request, apiKey, secret, body string) {
	// Единый timestamp для заголовка и подписи — должны совпадать!
	// Gate.io отклонит запрос если они разные.
	timestamp := NowUnix()

	// RawQuery = строка параметров URL, например "limit=10".
	// Включается в подпись — Gate.io проверяет query тоже.
	query := req.URL.RawQuery

	// Вычисляем HMAC-SHA512 подпись.
	sign := SignREST(
		secret,
		req.Method,   // "GET" или "POST"
		req.URL.Path, // "/api/v4/futures/usdt/accounts"
		query,        // "" или "limit=10"
		body,         // "" для GET, JSON для POST
		timestamp,
	)

	// Заголовки авторизации Gate.io.
	req.Header.Set("KEY", apiKey)
	req.Header.Set("SIGN", sign)
	// FormatInt конвертирует int64 → строку (основание 10).
	req.Header.Set("Timestamp", strconv.FormatInt(timestamp, 10))
}
