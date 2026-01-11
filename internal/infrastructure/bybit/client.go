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

// GetIndexPrice возвращает цену базового актива (например, BTC).
// Используем Linear (USDT Perp) тикеры как самый надежный источник Index Price.
func (c *Client) GetIndexPrice(ctx context.Context, symbol string) (decimal.Decimal, error) {
	// Если пришел "BTC", превращаем в "BTCUSDT"
	target := symbol
	if !strings.HasSuffix(target, "USDT") && !strings.HasSuffix(target, "USD") {
		target += "USDT"
	}

	params := map[string]string{
		"category": "linear",
		"symbol":   target,
	}

	var resp BaseResponse[TickerResponse]
	if err := c.sendPublicRequest(ctx, "GET", "/v5/market/tickers", params, &resp); err != nil {
		return decimal.Zero, err
	}

	if len(resp.Result.List) == 0 {
		return decimal.Zero, fmt.Errorf("index price not found for %s", target)
	}

	// Берем MarkPrice или IndexPrice из ответа
	return resp.Result.List[0].MarkPrice, nil
}

func (c *Client) GetMarkPrice(ctx context.Context, symbol string) (decimal.Decimal, error) {
	params := map[string]string{
		"category": "option",
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
		"category": "option",
		"symbol":   symbol,
	}

	var resp BaseResponse[PositionResponse]
	if err := c.sendPrivateRequest(ctx, creds, "GET", "/v5/position/list", params, nil, &resp); err != nil {
		return domain.Position{}, err
	}

	if len(resp.Result.List) == 0 {
		return domain.Position{}, nil // Позиции нет
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

func (c *Client) PlaceOrder(ctx context.Context, creds domain.APIKey, req domain.OrderRequest) (string, error) {
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

// --- Private Helpers ---

func (c *Client) sendPublicRequest(ctx context.Context, method, endpoint string, params map[string]string, result interface{}) error {
	var queryString string
	if len(params) > 0 {
		var parts []string
		for k, v := range params {
			parts = append(parts, fmt.Sprintf("%s=%s", k, v))
		}
		queryString = strings.Join(parts, "&")
	}

	fullURL := c.baseURL + endpoint
	if queryString != "" {
		fullURL += "?" + queryString
	}

	req, err := http.NewRequestWithContext(ctx, method, fullURL, nil)
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return c.decodeResponse(resp.Body, result)
}

func (c *Client) sendPrivateRequest(ctx context.Context, creds domain.APIKey, method, endpoint string, queryParams map[string]string, bodyParams map[string]interface{}, result interface{}) error {
	ts := fmt.Sprintf("%d", time.Now().UnixMilli())
	
	var queryString string
	if len(queryParams) > 0 {
		var parts []string
		for k, v := range queryParams {
			parts = append(parts, fmt.Sprintf("%s=%s", k, v))
		}
		queryString = strings.Join(parts, "&")
	}

	var bodyString string
	if method == "POST" && bodyParams != nil {
		jsonBytes, err := json.Marshal(bodyParams)
		if err != nil {
			return err
		}
		bodyString = string(jsonBytes)
	}

	var payload string
	if method == "GET" {
		payload = ts + creds.Key + RecvWindow + queryString
	} else {
		payload = ts + creds.Key + RecvWindow + bodyString
	}

	signature := generateSignature(payload, creds.Secret)

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

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-BAPI-API-KEY", creds.Key)
	req.Header.Set("X-BAPI-SIGN", signature)
	req.Header.Set("X-BAPI-TIMESTAMP", ts)
	req.Header.Set("X-BAPI-RECV-WINDOW", RecvWindow)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return c.decodeResponse(resp.Body, result)
}

func (c *Client) decodeResponse(body io.Reader, result interface{}) error {
	respBytes, err := io.ReadAll(body)
	if err != nil {
		return err
	}

	var base BaseResponse[interface{}]
	if err := json.Unmarshal(respBytes, &base); err != nil {
		return fmt.Errorf("failed to parse response: %v | Body: %s", err, string(respBytes))
	}

	if base.RetCode != 0 {
		return fmt.Errorf("bybit api error: [%d] %s", base.RetCode, base.RetMsg)
	}

	return json.Unmarshal(respBytes, result)
}

func generateSignature(payload, secret string) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(payload))
	return hex.EncodeToString(h.Sum(nil))
}