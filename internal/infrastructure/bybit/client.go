package bybit

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/romanzzaa/bybit-options-roller/internal/domain"
	"github.com/shopspring/decimal"
)

const (
	MainnetBaseURL = "https://api.bybit.com"
	TestnetBaseURL = "https://api-testnet.bybit.com"
	RecvWindow     = "5000"
)

// Client реализует domain.ExchangeAdapter
type Client struct {
	baseURL    string
	httpClient *http.Client
}

func NewClient(isTestnet bool) *Client {
	url := MainnetBaseURL
	if isTestnet {
		url = TestnetBaseURL
	}
	return &Client{
		baseURL:    url,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// --- Implementation of ExchangeAdapter ---

func (c *Client) GetMarkPrice(ctx context.Context, symbol string) (decimal.Decimal, error) {
	// Для опционов category=option, но марк-цена базового актива может быть в linear.
	// Пока предполагаем простой кейс получения тикера.
	params := map[string]string{
		"category": "option", // Или linear, зависит от того, чей MarkPrice нужен
		"symbol":   symbol,
	}
	
	var resp BaseResponse[TickerResponse]
	if err := c.sendPublicRequest(ctx, "GET", "/v5/market/tickers", params, &resp); err != nil {
		return decimal.Zero, err
	}

	if len(resp.Result.List) == 0 {
		return decimal.Zero, fmt.Errorf("symbol not found")
	}

	return resp.Result.List[0].MarkPrice, nil
}

func (c *Client) GetPosition(ctx context.Context, creds domain.APIKey, symbol string) (domain.Position, error) {
	params := map[string]string{
		"category": "option", // В рамках Options Roller мы работаем с опционами
		"symbol":   symbol,
	}

	var resp BaseResponse[PositionResponse]
	if err := c.sendPrivateRequest(ctx, creds, "GET", "/v5/position/list", params, nil, &resp); err != nil {
		return domain.Position{}, err
	}

	if len(resp.Result.List) == 0 {
		return domain.Position{}, nil // Позиции нет, возвращаем пустую
	}

	raw := resp.Result.List[0]
	return domain.Position{
		Symbol:        raw.Symbol,
		Side:          raw.Side,
		Qty:           raw.Size,
		EntryPrice:    raw.AvgPrice,
		MarkPrice:     raw.MarkPrice,
		UnrealizedPnL: raw.UnrealisedPnl,
	}, nil
}

func (c *Client) GetMarginInfo(ctx context.Context, creds domain.APIKey) (domain.MarginInfo, error) {
	params := map[string]string{
		"accountType": "UNIFIED",
	}

	var resp BaseResponse[WalletBalanceResponse]
	// Обрати внимание: wallet-balance это GET запрос
	if err := c.sendPrivateRequest(ctx, creds, "GET", "/v5/account/wallet-balance", params, nil, &resp); err != nil {
		return domain.MarginInfo{}, err
	}

	if len(resp.Result.List) == 0 {
		return domain.MarginInfo{}, fmt.Errorf("empty wallet balance")
	}

	wb := resp.Result.List[0]
	return domain.MarginInfo{
		TotalEquity:        wb.TotalEquity,
		TotalMarginBalance: wb.TotalMarginBalance,
		MMR:                wb.AccountMMRate,
	}, nil
}

func (c *Client) PlaceOrder(ctx context.Context, creds domain.APIKey, req domain.OrderRequest) (string, error) {
	// Подготовка JSON body для POST запроса
	bodyParams := map[string]interface{}{
		"category":    "option",
		"symbol":      req.Symbol,
		"side":        req.Side,
		"orderType":   req.OrderType,
		"qty":         req.Qty.String(),
		"orderLinkId": req.OrderLinkID,
	}

	if req.OrderType == "Limit" {
		bodyParams["price"] = req.Price.String()
	}
	if req.ReduceOnly {
		bodyParams["reduceOnly"] = true
	}

	var resp BaseResponse[PlaceOrderResponse]
	if err := c.sendPrivateRequest(ctx, creds, "POST", "/v5/order/create", nil, bodyParams, &resp); err != nil {
		return "", err
	}

	return resp.Result.OrderID, nil
}

// --- Private Helpers (Signing & Request Logic) ---

func (c *Client) sendPublicRequest(ctx context.Context, method, endpoint string, params map[string]string, result interface{}) error {
	// TODO: Реализовать, если понадобится. Пока копия sendPrivateRequest без заголовков подписи
	// Public endpoint просто формирует URL с query params
	return nil 
}

// sendPrivateRequest выполняет подписанный запрос (HMAC SHA256)
func (c *Client) sendPrivateRequest(ctx context.Context, creds domain.APIKey, method, endpoint string, queryParams map[string]string, bodyParams map[string]interface{}, result interface{}) error {
	ts := fmt.Sprintf("%d", time.Now().UnixMilli())
	
	// 1. Формирование Query String (сортировка ключей не обязательна для Bybit, но полезна для дебага)
	var queryString string
	if len(queryParams) > 0 {
		var keys []string
		for k := range queryParams {
			keys = append(keys, k)
		}
		// sort.Strings(keys) // Bybit V5 не требует строгой сортировки, но это good practice
		var parts []string
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%s=%s", k, queryParams[k]))
		}
		queryString = strings.Join(parts, "&")
	}

	// 2. Формирование Body String
	var bodyString string
	if method == "POST" && bodyParams != nil {
		jsonBytes, err := json.Marshal(bodyParams)
		if err != nil {
			return err
		}
		bodyString = string(jsonBytes)
	}

	// 3. Генерация подписи
	// Правило: timestamp + api_key + recv_window + (queryString OR jsonBodyString)
	var payload string
	if method == "GET" {
		payload = ts + creds.Key + RecvWindow + queryString
	} else {
		payload = ts + creds.Key + RecvWindow + bodyString
	}

	signature := generateSignature(payload, creds.SecretEnc) // Внимание: тут должен быть РАСШИФРОВАННЫЙ секрет. В реальном коде добавь дешифровку.

	// 4. Создание запроса
	fullURL := c.baseURL + endpoint
	if queryString != "" {
		fullURL += "?" + queryString
	}

	var reqBody io.Reader
	if bodyString != "" {
		reqBody = bytes.NewBufferString(bodyString)
	}

	req, err := http.NewRequestWithContext(ctx, method, fullURL, reqBody)
	if err != nil {
		return err
	}

	// 5. Заголовки
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-BAPI-API-KEY", creds.Key)
	req.Header.Set("X-BAPI-SIGN", signature)
	req.Header.Set("X-BAPI-TIMESTAMP", ts)
	req.Header.Set("X-BAPI-RECV-WINDOW", RecvWindow)

	// 6. Отправка и чтение
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	// 7. Парсинг ошибки API
	// Сначала читаем в базовую структуру, чтобы проверить RetCode
	var base BaseResponse[interface{}]
	if err := json.Unmarshal(respBytes, &base); err != nil {
		return fmt.Errorf("failed to parse response: %v | Body: %s", err, string(respBytes))
	}

	if base.RetCode != 0 {
		return fmt.Errorf("bybit api error: [%d] %s", base.RetCode, base.RetMsg)
	}

	// Если ок, парсим в целевую структуру
	return json.Unmarshal(respBytes, result)
}

func generateSignature(payload, secret string) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(payload))
	return hex.EncodeToString(h.Sum(nil))
}