// Этот файл содержит вспомогательные функции для HTTP запросов к Gate.io.
// Основная задача — формирование заголовков авторизации для приватных endpoints.
package utils

import (
	// fmt — форматирование строк
	"fmt"

	// net/http — стандартная библиотека Go для HTTP.
	// Содержит типы Request, Response, Header и методы для работы с ними.
	"net/http"

	// strconv — конвертация между типами данных.
	// Используем для преобразования int64 timestamp в строку для заголовка.
	"strconv"
)

// AuthHeaders формирует и добавляет заголовки авторизации Gate.io
// к существующему HTTP запросу.
//
// Gate.io требует следующие заголовки для приватных endpoints:
//   KEY       : API ключ
//   SIGN      : HMAC-SHA512 подпись запроса
//   Timestamp : время запроса в секундах Unix
//
// Параметры:
//   - req    : указатель на HTTP запрос который нужно авторизовать.
//              Используем указатель чтобы изменить оригинальный запрос
//              а не его копию — это важно! Без * изменения не сохранятся.
//   - apiKey : API ключ из .env (GATE_API_KEY)
//   - secret : API Secret из .env (GATE_API_SECRET)
//   - body   : тело запроса в виде строки (для GET запросов — "")
//
// Функция ничего не возвращает — она модифицирует запрос напрямую
// через указатель, добавляя нужные заголовки.
func AuthHeaders(req *http.Request, apiKey, secret, body string) {
	// ШАГ 1: Получаем текущий timestamp в секундах Unix.
	// Одно время для всех заголовков — важна консистентность!
	// Gate.io отклонит запрос если timestamp в заголовке и в подписи разные.
	timestamp := NowUnix()

	// ШАГ 2: Извлекаем query string из URL запроса.
	// req.URL.RawQuery возвращает строку параметров запроса.
	// Например для URL "/api/v4/futures/usdt/positions?limit=10"
	// RawQuery = "limit=10"
	// Для запросов без параметров RawQuery = "" (пустая строка).
	query := req.URL.RawQuery

	// ШАГ 3: Вычисляем HMAC-SHA512 подпись через наш utils/hmac.go.
	// req.Method — HTTP метод запроса ("GET", "POST" и т.д.)
	// req.URL.Path — путь запроса без хоста, например "/api/v4/futures/usdt/accounts"
	sign := SignREST(
		secret,
		req.Method,   // "GET" или "POST"
		req.URL.Path, // "/api/v4/futures/usdt/accounts"
		query,        // "" или "limit=10&offset=0"
		body,         // "" для GET, JSON строка для POST
		timestamp,    // текущий Unix timestamp
	)

	// ШАГ 4: Добавляем заголовки авторизации к запросу.
	// req.Header.Set(key, value) устанавливает заголовок.
	// Используем Set а не Add — Set перезаписывает если заголовок уже есть,
	// Add добавляет ещё один заголовок с тем же именем (нам не нужно).

	// KEY — наш API ключ, идентифицирует нас на бирже
	req.Header.Set("KEY", apiKey)

	// SIGN — подпись запроса, доказывает что запрос не был изменён
	req.Header.Set("SIGN", sign)

	// Timestamp — время запроса, Gate.io отклоняет запросы
	// с timestamp старше 60 секунд (защита от replay атак)
	req.Header.Set("Timestamp", strconv.FormatInt(timestamp, 10))
	// strconv.FormatInt(timestamp, 10) конвертирует int64 в строку
	// в десятичной системе счисления (основание 10).
	// Например: 1741523200 → "1741523200"

	// Content-Type сообщает бирже что мы отправляем JSON.
	// Нужен для POST запросов, для GET не критично но хорошая практика.
	req.Header.Set("Content-Type", "application/json")

	// Accept сообщает бирже что мы ожидаем JSON в ответе.
	req.Header.Set("Accept", "application/json")

	// User-Agent идентифицирует наш клиент.
	// Хорошая практика — указывать название и версию своего приложения.
	req.Header.Set("User-Agent", fmt.Sprintf("dtrader-6/bot"))
}
