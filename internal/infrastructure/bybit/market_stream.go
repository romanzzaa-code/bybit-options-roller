package bybit

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"github.com/gorilla/websocket"
	"github.com/shopspring/decimal"
	"bybit-options-roller/internal/domain" // Поправь путь под свой проект
)

type MarketStream struct {
	url    string
	conn   *websocket.Conn
	ctx    context.Context
	cancel context.CancelFunc
}

func NewMarketStream(testnet bool) *MarketStream {
	host := "stream.bybit.com"
	if testnet {
		host = "stream-testnet.bybit.com"
	}
	
	// Для опционов индексная цена часто берется из линейного потока (USDT)
	u := url.URL{Scheme: "wss", Host: host, Path: "/v5/public/linear"}
	
	ctx, cancel := context.WithCancel(context.Background())
	return &MarketStream{
		url:    u.String(),
		ctx:    ctx,
		cancel: cancel,
	}
}

func (s *MarketStream) Subscribe(symbols []string) (<-chan domain.PriceUpdate, error) {
	out := make(chan domain.PriceUpdate, 100)

	// 1. Подключение
	conn, _, err := websocket.DefaultDialer.Dial(s.url, nil)
	if err != nil {
		return nil, fmt.Errorf("ws dial error: %w", err)
	}
	s.conn = conn

	// 2. Формируем запрос подписки (Bybit V5 protocol)
	// Для получения Index Price используется топик "indexPrice.SYMBOL"
	args := make([]string, len(symbols))
	for i, sym := range symbols {
		args[i] = fmt.Sprintf("indexPrice.%s", sym)
	}

	subRequest := map[string]interface{}{
		"op":   "subscribe",
		"args": args,
	}

	if err := s.conn.WriteJSON(subRequest); err != nil {
		return nil, err
	}

	// 3. Запускаем чтение в отдельной горутине
	go s.readLoop(out)

	return out, nil
}

func (s *MarketStream) readLoop(out chan domain.PriceUpdate) {
	defer close(out)
	defer s.conn.Close()

	for {
		select {
		case <-s.ctx.Done():
			return
		default:
			_, message, err := s.conn.ReadMessage()
			if err != nil {
				// В реальном проекте здесь должен быть Reconnect logic
				return
			}

			// Парсим ответ (упрощенно)
			var resp struct {
				Topic string `json:"topic"`
				Data  struct {
					Symbol     string `json:"symbol"`
					IndexPrice string `json:"indexPrice"`
				} `json:"data"`
			}

			if err := json.Unmarshal(message, &resp); err != nil {
				continue
			}

			if resp.Data.IndexPrice != "" {
				price, _ := decimal.NewFromString(resp.Data.IndexPrice)
				out <- domain.PriceUpdate{
					Symbol: resp.Data.Symbol,
					Price:  price,
					Time:   time.Now(),
				}
			}
		}
	}
}

func (s *MarketStream) Close() error {
	s.cancel()
	if s.conn != nil {
		return s.conn.Close()
	}
	return nil
}