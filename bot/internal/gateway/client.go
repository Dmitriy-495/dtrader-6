// Пакет gateway отвечает за всё взаимодействие с биржей Gate.io.
// Этот файл реализует базовый HTTP клиент для REST API запросов.
//
// Архитектура пакета:
//   constants.go — константы пакета
//   client.go    — низкоуровневый HTTP клиент (этот файл)
//   rest.go      — высокоуровневые методы: Ping, Balance, Positions
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/Dmitriy-495/dtrader-6/bot/internal/utils"
)

// Client — HTTP клиент для REST API Gate.io.
// Создаётся один раз при старте и переиспользуется для всех запросов.
type Client struct {
	// apiKey — приватное поле, недоступно вне пакета gateway.
	apiKey string
	// secret — приватное поле, аналогично apiKey.
	secret string
	// baseURL — базовый URL REST API, например "https://api.gateio.ws/api/v4".
	baseURL string
	// httpClient — стандартный HTTP клиент Go с таймаутом.
	// Указатель *http.Client — содержит мьютексы, копировать нельзя.
	httpClient *http.Client
}

// NewClient создаёт новый Client для работы с Gate.io REST API.
// Конструктор — стандартный паттерн Go: New+ИмяТипа.
func NewClient(apiKey, secret, baseURL string) *Client {
	return &Client{
		apiKey:  apiKey,
		secret:  secret,
		baseURL: baseURL,
		// Таймаут из константы — не магическое число!
		httpClient: &http.Client{
			Timeout: requestTimeout,
		},
	}
}

// buildURL формирует полный URL из baseURL + endpoint + query.
// Приватный метод — используется только внутри пакета gateway.
//
// Примеры:
//   buildURL("/futures/usdt/accounts", "")         → "https://.../futures/usdt/accounts"
//   buildURL("/futures/usdt/contracts", "limit=1") → "https://.../futures/usdt/contracts?limit=1"
func (c *Client) buildURL(endpoint, query string) string {
	url := c.baseURL + endpoint
	if query != "" {
		url = url + "?" + query
	}
	return url
}

// setCommonHeaders устанавливает стандартные заголовки для всех запросов.
// Приватный метод — убирает дублирование между Get, GetPublic, Post.
// Content-Type, Accept и User-Agent нужны всем запросам без исключения.
func (c *Client) setCommonHeaders(req *http.Request) {
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", contentType)
	req.Header.Set("User-Agent", userAgent)
}

// readResponse проверяет статус HTTP ответа и десериализует JSON в result.
// Приватный метод — убирает дублирование из Get, GetPublic, Post.
func (c *Client) readResponse(resp *http.Response, endpoint string, result interface{}) error {
	// Gate.io возвращает 200 для GET и 200/201 для POST.
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		// Читаем тело ответа для понятного сообщения об ошибке.
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Gate.io вернул статус %d для %s: %s",
			resp.StatusCode, endpoint, string(body))
	}

	// Декодируем JSON прямо из тела ответа без промежуточного буфера.
	if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
		return fmt.Errorf("ошибка десериализации ответа %s: %w", endpoint, err)
	}

	return nil
}

// Get выполняет авторизованный GET запрос к Gate.io REST API.
//
// Параметры:
//   - ctx      : контекст — позволяет отменить запрос по таймауту
//   - endpoint : путь без baseURL, например "/futures/usdt/accounts"
//   - query    : параметры запроса, например "limit=10" или ""
//   - result   : указатель на структуру для десериализации ответа
func (c *Client) Get(ctx context.Context, endpoint, query string, result interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.buildURL(endpoint, query), nil)
	if err != nil {
		return fmt.Errorf("ошибка создания GET запроса %s: %w", endpoint, err)
	}

	// Сначала общие заголовки, потом авторизация.
	// AuthHeaders перезапишет Content-Type если нужно — это нормально.
	c.setCommonHeaders(req)
	utils.AuthHeaders(req, c.apiKey, c.secret, "")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("ошибка выполнения GET запроса %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	return c.readResponse(resp, endpoint, result)
}

// GetPublic выполняет НЕ авторизованный GET запрос к Gate.io REST API.
// Используется для публичных endpoints: ping, список контрактов и т.д.
func (c *Client) GetPublic(ctx context.Context, endpoint, query string, result interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.buildURL(endpoint, query), nil)
	if err != nil {
		return fmt.Errorf("ошибка создания публичного GET запроса %s: %w", endpoint, err)
	}

	// Только общие заголовки — без подписи!
	c.setCommonHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("ошибка выполнения публичного GET запроса %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	return c.readResponse(resp, endpoint, result)
}

// Post выполняет авторизованный POST запрос к Gate.io REST API.
// Используется для создания ордеров и других операций записи.
// Фундамент для trader сервиса — пока не используется активно.
func (c *Client) Post(ctx context.Context, endpoint string, payload, result interface{}) error {
	// Сериализуем payload в JSON байты.
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("ошибка сериализации тела запроса %s: %w", endpoint, err)
	}

	// bytes.NewReader создаёт io.Reader из байтового среза.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.buildURL(endpoint, ""), bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("ошибка создания POST запроса %s: %w", endpoint, err)
	}

	// Общие заголовки + авторизация с телом запроса.
	// Gate.io проверяет что тело не было изменено после подписания!
	c.setCommonHeaders(req)
	utils.AuthHeaders(req, c.apiKey, c.secret, string(bodyBytes))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("ошибка выполнения POST запроса %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	return c.readResponse(resp, endpoint, result)
}
