package bybit

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/romanzzaa/bybit-options-roller/internal/domain"
	"github.com/shopspring/decimal"
)

const (
	// Public Linear Stream (USDT Perpetual) - самый надежный источник Index/Mark Price
	MainnetLinearParams = "wss://stream.bybit.com/v5/public/linear"
	TestnetLinearParams = "wss://stream-testnet.bybit.com/v5/public/linear"
	
	reconnectDelay = 5 * time.Second
	pingInterval   = 20 * time.Second
)

type MarketStream struct {
	url      string
	logger   *slog.Logger
	conn     *websocket.Conn
	mu       sync.Mutex
	stopChan chan struct{}
	
	// Храним список активных подписок для автоматического реконнекта
	activeSubs []string 
	subsMu     sync.RWMutex
}

func NewMarketStream(isTestnet bool) *MarketStream {
	url := MainnetLinearParams
	if isTestnet {
		url = TestnetLinearParams
	}

	return &MarketStream{
		url:      url,
		logger:   slog.Default().With("component", "market_stream"),
		stopChan: make(chan struct{}),
		activeSubs: make([]string, 0),
	}
}

// Subscribe сохраняет символы и запускает процесс чтения
func (s *MarketStream) Subscribe(symbols []string) (<-chan domain.PriceUpdateEvent, error) {
	out := make(chan domain.PriceUpdateEvent, 100)
	
	// Сохраняем начальные символы
	s.subsMu.Lock()
	s.activeSubs = symbols
	s.subsMu.Unlock()

	go s.maintainConnection(out)

	return out, nil
}

// AddSubscriptions добавляет новые символы "на лету" без разрыва соединения
func (s *MarketStream) AddSubscriptions(symbols []string) error {
	s.subsMu.Lock()
	// Простая дедупликация
	var newSubs []string
	for _, newSym := range symbols {
		exists := false
		for _, oldSym := range s.activeSubs {
			if newSym == oldSym {
				exists = true
				break
			}
		}
		if !exists {
			s.activeSubs = append(s.activeSubs, newSym)
			newSubs = append(newSubs, newSym)
		}
	}
	s.subsMu.Unlock()

	if len(newSubs) == 0 {
		return nil
	}

	// Если соединение активно, отправляем команду подписки немедленно
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn != nil {
		return s.sendSubscribe(newSubs)
	}
	return nil
}

func (s *MarketStream) maintainConnection(out chan<- domain.PriceUpdateEvent) {
	for {
		select {
		case <-s.stopChan:
			return
		default:
			// Берем текущий список всех подписок для восстановления сессии
			s.subsMu.RLock()
			subs := s.activeSubs
			s.subsMu.RUnlock()

			if err := s.connectAndListen(subs, out); err != nil {
				s.logger.Error("Connection lost or failed", "err", err)
			}
			
			s.logger.Info("Reconnecting in 5 seconds...")
			time.Sleep(reconnectDelay)
		}
	}
}

func (s *MarketStream) connectAndListen(symbols []string, out chan<- domain.PriceUpdateEvent) error {
	s.logger.Info("Connecting to Bybit Linear Stream...", "url", s.url)

	conn, _, err := websocket.DefaultDialer.Dial(s.url, nil)
	if err != nil {
		return err
	}
	
	s.mu.Lock()
	s.conn = conn
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		if s.conn != nil {
			s.conn.Close()
			s.conn = nil
		}
		s.mu.Unlock()
	}()

	// Сразу подписываемся на все накопленные символы
	if len(symbols) > 0 {
		if err := s.sendSubscribe(symbols); err != nil {
			return err
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.heartbeat(ctx)

	// Цикл чтения
	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("read error: %w", err)
		}

		var rawMsg map[string]interface{}
		if err := json.Unmarshal(message, &rawMsg); err != nil {
			continue
		}

		// Игнорируем ответы на ping/subscribe
		if _, ok := rawMsg["op"]; ok {
			continue 
		}

		var event WsTickerEvent
		if err := json.Unmarshal(message, &event); err != nil {
			continue
		}

		// Linear Ticker Data Processing
		if event.Topic != "" && len(event.Data) > 0 {
			data := event.Data[0]
			
			// Используем MarkPrice как наиболее надежный источник для триггера
			price := data.MarkPrice
			if price.IsZero() {
				price = data.LastPrice
			}

			// Формируем событие. 
			// ВАЖНО: Symbol здесь будет "BTCUSDT". Менеджер должен ожидать именно это.
			updateEvent := domain.PriceUpdateEvent{
				Symbol: data.Symbol,
				Price:  price,
				Time:   time.Now(),
				Source: "bybit-linear-ws",
			}

			select {
			case out <- updateEvent:
			default:
				// Если канал переполнен, пропускаем устаревший тик
			}
		}
	}
}

func (s *MarketStream) sendSubscribe(symbols []string) error {
	if len(symbols) == 0 {
		return nil
	}
	
	args := make([]string, len(symbols))
	for i, sym := range symbols {
		// Подписка на тикеры фьючерсов
		args[i] = "tickers." + sym 
	}

	req := map[string]interface{}{
		"op":   "subscribe",
		"args": args,
	}
	
	s.logger.Info("Sending subscription request", "topics", args)

	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn.WriteJSON(req)
}

func (s *MarketStream) heartbeat(ctx context.Context) {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.mu.Lock()
			if s.conn != nil {
				if err := s.conn.WriteJSON(map[string]string{"op": "ping"}); err != nil {
					s.logger.Error("Ping failed", "err", err)
				}
			}
			s.mu.Unlock()
		}
	}
}

// WsTickerEvent соответствует структуре сообщения из Linear Stream
type WsTickerEvent struct {
	Topic string `json:"topic"`
	Data  []struct {
		Symbol    string          `json:"symbol"`
		LastPrice decimal.Decimal `json:"lastPrice"`
		MarkPrice decimal.Decimal `json:"markPrice"`
	} `json:"data"`
}