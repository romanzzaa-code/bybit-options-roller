package bybit

import "github.com/shopspring/decimal"

// BaseResponse - стандартная обертка ответа Bybit
type BaseResponse[T any] struct {
	RetCode int    `json:"retCode"`
	RetMsg  string `json:"retMsg"`
	Result  T      `json:"result"`
}

// --- DTOs для конкретных эндпоинтов ---

// TickerResponse - для получения цены (GetMarkPrice)
type TickerResponse struct {
	List []struct {
		Symbol    string          `json:"symbol"`
		MarkPrice decimal.Decimal `json:"markPrice"`
		LastPrice decimal.Decimal `json:"lastPrice"`
	} `json:"list"`
}

// PositionResponse - для получения позиций (GetPosition)
type PositionResponse struct {
	List []struct {
		Symbol       string          `json:"symbol"`
		Side         string          `json:"side"` // "Buy" or "Sell"
		Size         decimal.Decimal `json:"size"`
		AvgPrice     decimal.Decimal `json:"avgPrice"`
		MarkPrice    decimal.Decimal `json:"markPrice"`
		UnrealisedPnl decimal.Decimal `json:"unrealisedPnl"`
	} `json:"list"`
}

// WalletBalanceResponse - для маржи (GetMarginInfo)
type WalletBalanceResponse struct {
	List []struct {
		TotalEquity        decimal.Decimal `json:"totalEquity"`
		TotalMarginBalance decimal.Decimal `json:"totalMarginBalance"`
		AccountMMRate      decimal.Decimal `json:"accountMMRate"` // MMR аккаунта
	} `json:"list"`
}

// PlaceOrderResponse - ответ на создание ордера
type PlaceOrderResponse struct {
	OrderID     string `json:"orderId"`
	OrderLinkID string `json:"orderLinkId"`
}

type InstrumentInfoResponse struct {
	RetCode int    `json:"retCode"`
	RetMsg  string `json:"retMsg"`
	Result  struct {
		List []struct {
			Symbol          string `json:"symbol"`
			Status          string `json:"status"` // "Trading"
			BaseCoin        string `json:"baseCoin"`
			QuoteCoin       string `json:"quoteCoin"`
			OptionType      string `json:"optionType"` // Call/Put
			StrikePrice     string `json:"strikePrice"`
			ActivationDate  string `json:"activationDate"`
			DeliveryTime    string `json:"deliveryTime"`
		} `json:"list"`
	} `json:"result"`
}