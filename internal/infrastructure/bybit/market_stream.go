package bybit

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/romanzzaa/bybit-options-roller/internal/domain"

	// УДАЛИТЕ ЛЮБЫЕ УПОМИНАНИЯ "internal/infrastructure/bybit" ЗДЕСЬ!
	
	"github.com/gorilla/websocket"
	"github.com/shopspring/decimal"
)

const (
	// Bybit Testnet Option Stream
	wsURL = "wss://stream-testnet.bybit.com/v5/public/option"
	// Reconnect settings
	reconnectDelay = 5 * time.Second
	pingInterval   = 20 * time.Second
)

type MarketStream struct {
	isTestnet bool
	logger    *slog.Logger
	conn      *websocket.Conn
	mu        sync.Mutex // Защита записи в сокет

	// Каналы для управления
	stopChan chan struct{}
}

func NewMarketStream(isTestnet bool) *MarketStream {
	return &MarketStream{
		isTestnet: isTestnet,
		logger:    slog.Default().With("component", "market_stream"),
		stopChan:  make(chan struct{}),
	}
}

// Subscribe запускает вечный цикл поддержания соединения
func (s *MarketStream) Subscribe(symbols []string) (<-chan domain.PriceUpdateEvent, error) {
	out := make(chan domain.PriceUpdateEvent, 100)

	go s.maintainConnection(symbols, out)

	return out, nil
}


// maintainConnection - главный цикл жизнеобеспечения (Reconnect Loop)
func (s *MarketStream) maintainConnection(symbols []string, out chan<- domain.PriceUpdateEvent) {
	for {
		select {
		case <-s.stopChan:
			return
		default:
			// 1. Попытка подключения
			if err := s.connectAndListen(symbols, out); err != nil {
				s.logger.Error("Connection lost or failed", "err", err)
			}

			// 2. Ожидание перед реконнектом (Backoff)
			s.logger.Info("Reconnecting in 5 seconds...")
			time.Sleep(reconnectDelay)
		}
	}
}

// connectAndListen - одна сессия подключения
func (s *MarketStream) connectAndListen(symbols []string, out chan<- domain.PriceUpdateEvent) error {
	s.logger.Info("Connecting to Bybit Stream...", "url", wsURL)

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.conn = conn
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.conn.Close()
		s.conn = nil
		s.mu.Unlock()
	}()

	// 1. Отправляем подписку
	if err := s.sendSubscribe(symbols); err != nil {
		return err
	}

	// 2. Запускаем Heartbeat (Ping) в отдельной горутине
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.heartbeat(ctx)

	// 3. Читаем сообщения (Read Loop)
	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("read error: %w", err)
		}

		// Парсим сырой JSON, чтобы определить тип сообщения
		var rawMsg map[string]interface{}
		if err := json.Unmarshal(message, &rawMsg); err != nil {
			s.logger.Error("Failed to unmarshal raw JSON", "err", err)
			continue
		}

		// Игнорируем ответы на ping/subscribe
		if op, ok := rawMsg["op"].(string); ok {
			if op == "pong" || op == "subscribe" {
				continue 
			}
		}

		// Парсим данные тикера
		var event WsTickerEvent
		if err := json.Unmarshal(message, &event); err != nil {
			// Это нормально для других типов сообщений, но логируем на всякий случай
			// s.logger.Debug("Skipping message", "msg", string(message))
			continue
		}

		// Преобразуем в доменное событие
		if event.Topic != "" && len(event.Data) > 0 {
			updateEvent := domain.PriceUpdateEvent{
				Symbol: event.Data[0].Symbol,
				Price:  event.Data[0].LastPrice, // Используем decimal из DTO
				Time:   time.Now(),
				Source: "bybit-ws",
			}
			// Non-blocking send
			select {
			case out <- updateEvent:
			default:
				s.logger.Warn("Output channel full, dropping price update", "symbol", updateEvent.Symbol)
			}
		}
	}
}

func (s *MarketStream) sendSubscribe(symbols []string) error {
	// Формируем топики: "tickers.ETH-29DEC-2000-C", но нам нужен Index Price или Mark Price?
	// В спецификации вы хотели Index Price. В Bybit Option это "tickers.{symbol}" дает Mark/Index/Last.
	
	// ВНИМАНИЕ: Для опционов топик обычно 'tickers.{symbol}'. 
	// Если вы хотите следить за всеми ETH опционами, нужно подписываться иначе.
	// Для MVP подписываемся на конкретные символы задач.
	
	args := make([]string, len(symbols))
	for i, sym := range symbols {
		args[i] = "tickers." + sym
	}

	req := map[string]interface{}{
		"op":   "subscribe",
		"args": args,
	}

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
				// Bybit V5 Ping format
				ping := map[string]string{"op": "ping"}
				if err := s.conn.WriteJSON(ping); err != nil {
					s.logger.Error("Ping failed", "err", err)
					// Ошибка записи приведет к разрыву в ReadLoop, здесь просто логируем
				}
			}
			s.mu.Unlock()
		}
	}
}

// Вспомогательная структура для парсинга (нужно добавить в этот файл или dto.go)
type WsTickerEvent struct {
	Topic string `json:"topic"`
	Data  []struct {
		Symbol    string          `json:"symbol"`
		LastPrice decimal.Decimal `json:"lastPrice"`
		MarkPrice decimal.Decimal `json:"markPrice"`
	} `json:"data"`
	
}

func (s *MarketStream) AddSubscriptions(symbols []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.conn == nil {
		// Если соединения еще нет, просто выходим.
		return nil
	}

	// Отправляем команду subscribe в существующий сокет
	return s.sendSubscribe(symbols)
}