// Пакет utils содержит переиспользуемые вспомогательные функции.
// Этот файл реализует подпись запросов для Gate.io API v4.
//
// Gate.io использует два разных формата подписи:
// 1. REST API  — подпись включает метод, путь, query, хэш тела и timestamp
// 2. WebSocket — подпись включает channel, event и timestamp
//
// Оба метода используют алгоритм HexEncode(HMAC_SHA512(secret, message)).
package utils

import (
	// crypto/hmac — стандартная библиотека Go для HMAC подписи.
	// HMAC (Hash-based Message Authentication Code) — это способ проверить
	// что сообщение не было изменено и отправлено именно нами.
	"crypto/hmac"

	// crypto/sha512 — алгоритм хэширования SHA512.
	// Gate.io требует именно SHA512 а не SHA256.
	"crypto/sha512"

	// encoding/hex — преобразует байты в шестнадцатеричную строку.
	// Gate.io ожидает подпись в виде hex строки, например:
	// "a3f2c1..." а не сырые байты.
	"encoding/hex"

	// fmt — форматирование строк для формирования signature_string
	"fmt"
)

// SignREST формирует подпись для REST API запросов Gate.io.
//
// Gate.io REST API требует следующий формат signature_string:
//   METHOD\n/api/v4/path\nquery_string\nSHA512(body)\ntimestamp
//
// Параметры:
//   - secret    : API Secret из .env (GATE_API_SECRET)
//   - method    : HTTP метод в верхнем регистре ("GET", "POST")
//   - path      : путь запроса БЕЗ хоста, например "/api/v4/futures/usdt/accounts"
//   - query     : строка параметров запроса, например "limit=10&offset=0"
//                 пустая строка "" если параметров нет
//   - body      : тело запроса в виде строки (для GET запросов — пустая строка "")
//   - timestamp : текущее время в секундах Unix (int64)
//
// Возвращает hex строку подписи которую нужно передать в заголовке SIGN.
func SignREST(secret, method, path, query, body string, timestamp int64) string {
	// ШАГ 1: Хэшируем тело запроса через SHA512.
	// Для GET запросов body = "", SHA512("") всегда равен константе:
	// cf83e1357eefb8bdf1542850d66d8007d620e4050b5715dc83f4a921d36ce9ce47d0d13c5d85f2b0ff8318d2877eec2f63b931bd47417a81a538327af927da3e
	//
	// sha512.New() создаёт новый хэшер.
	// Write() записывает данные в хэшер (можно вызывать несколько раз).
	// Sum(nil) возвращает итоговый хэш в виде байтового среза []byte.
	bodyHasher := sha512.New()
	bodyHasher.Write([]byte(body))                    // записываем тело в хэшер
	bodyHash := hex.EncodeToString(bodyHasher.Sum(nil)) // конвертируем байты в hex строку

	// ШАГ 2: Формируем signature_string по требованию Gate.io.
	// \n — символ новой строки, разделитель между частями строки.
	// fmt.Sprintf форматирует строку подставляя значения вместо %s и %d.
	// %s — строковый аргумент, %d — целочисленный аргумент.
	signatureString := fmt.Sprintf("%s\n%s\n%s\n%s\n%d",
		method,    // например "GET"
		path,      // например "/api/v4/futures/usdt/accounts"
		query,     // например "" или "limit=10"
		bodyHash,  // SHA512 хэш тела запроса
		timestamp, // например 1741523200
	)

	// ШАГ 3: Вычисляем HMAC-SHA512 подпись.
	// hmac.New(sha512.New, key) создаёт HMAC хэшер с нашим секретным ключом.
	// []byte(secret) конвертирует строку в байтовый срез —
	// все криптографические функции Go работают с байтами а не строками.
	mac := hmac.New(sha512.New, []byte(secret))

	// Записываем signature_string в HMAC хэшер.
	// Write никогда не возвращает ошибку для hmac — можно игнорировать.
	mac.Write([]byte(signatureString))

	// Sum(nil) возвращает итоговый HMAC в виде байтового среза.
	// hex.EncodeToString конвертирует байты в hex строку.
	// Именно эту строку передаём в заголовке SIGN каждого запроса.
	return hex.EncodeToString(mac.Sum(nil))
}

// SignWS формирует подпись для WebSocket запросов Gate.io.
//
// Gate.io WebSocket API требует другой формат signature_string:
//   channel=<channel>&event=<event>&time=<timestamp>
//
// Параметры:
//   - secret    : API Secret из .env (GATE_API_SECRET)
//   - channel   : название канала, например "futures.orders"
//   - event     : тип события, например "subscribe"
//   - timestamp : текущее время в секундах Unix (int64)
//
// Возвращает hex строку подписи которую нужно передать в поле auth.SIGN.
// TODO: используется в gateway/ws.go (WebSocket авторизация)
func SignWS(secret, channel, event string, timestamp int64) string {
	// Формируем signature_string для WebSocket.
	// Формат отличается от REST — нет метода, пути и хэша тела.
	signatureString := fmt.Sprintf("channel=%s&event=%s&time=%d",
		channel,   // например "futures.orders"
		event,     // например "subscribe"
		timestamp, // например 1741523200
	)

	// Вычисляем HMAC-SHA512 — алгоритм тот же что и для REST.
	mac := hmac.New(sha512.New, []byte(secret))
	mac.Write([]byte(signatureString))
	return hex.EncodeToString(mac.Sum(nil))
}
