// Этот файл содержит константы пакета gateway.
// Все магические числа выносим сюда — единое место для изменений.
package gateway

import "time"

const (
	// requestTimeout — таймаут для всех HTTP запросов к Gate.io.
	// 10 секунд — разумный баланс между ожиданием и реакцией на сбой.
	// Используется в NewClient и в main.go через gateway.RequestTimeout.
	requestTimeout = 10 * time.Second

	// userAgent — идентификатор нашего клиента для Gate.io.
	userAgent = "dtrader-6/bot"

	// contentType — стандартный Content-Type для JSON API.
	contentType = "application/json"
)

// RequestTimeout экспортируемая константа для использования в main.go.
// Экспортируем (заглавная буква) чтобы main.go не дублировал это значение.
// Таймаут контекстов в main.go должен совпадать с таймаутом HTTP клиента.
const RequestTimeout = requestTimeout
