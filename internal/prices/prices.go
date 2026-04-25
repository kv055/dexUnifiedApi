package prices

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"time"

	gorillaws "github.com/gorilla/websocket"
)

const defaultPair = "BTC/EUR"

type Manager struct {
	mu       sync.RWMutex
	prices   map[string]float64
	feeds    []string
	onUpdate func(string)
}

func NewManager(feeds []string) *Manager {
	if len(feeds) == 0 {
		feeds = []string{"kraken", "binance", "bitstamp", "coinbase"}
	}
	cp := make([]string, len(feeds))
	copy(cp, feeds)
	return &Manager{prices: map[string]float64{}, feeds: cp}
}

func (m *Manager) SetUpdateCallback(fn func(string)) {
	m.onUpdate = fn
}

func (m *Manager) Get(cex string) float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.prices[cex]
}

func (m *Manager) Set(cex string, price float64) {
	m.mu.Lock()
	m.prices[cex] = price
	m.mu.Unlock()
	if m.onUpdate != nil {
		m.onUpdate(cex)
	}
}

func (m *Manager) Start(ctx context.Context) {
	for _, cex := range m.feeds {
		go m.runFeed(ctx, cex)
	}
}

func (m *Manager) runFeed(ctx context.Context, cex string) {
	backoff := time.Second
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		var err error
		switch cex {
		case "kraken":
			err = m.runKrakenFeed(ctx, cex)
		case "binance":
			err = m.runBinanceFeed(ctx, cex)
		case "bitstamp":
			err = m.runBitstampFeed(ctx, cex)
		case "coinbase":
			err = m.runCoinbaseFeed(ctx, cex)
		default:
			return
		}

		if err != nil {
			fmt.Printf("[%sFeed] error: %v, retrying in %s\n", cex, err, backoff)
		}
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return
		}
		if backoff < 30*time.Second {
			backoff *= 2
		} else {
			backoff = time.Second
		}
	}
}

func (m *Manager) runKrakenFeed(ctx context.Context, cex string) error {
	ws, _, err := gorillaws.DefaultDialer.DialContext(ctx, "wss://ws.kraken.com/v2", nil)
	if err != nil {
		return err
	}
	defer ws.Close()
	fmt.Printf("[%sFeed] connected\n", cex)

	sub := fmt.Sprintf(`{"method":"subscribe","params":{"channel":"ticker","symbol":["%s"]}}`, defaultPair)
	if err := ws.WriteMessage(gorillaws.TextMessage, []byte(sub)); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		_, msgBytes, err := ws.ReadMessage()
		if err != nil {
			return err
		}
		var envelope struct {
			Channel string `json:"channel"`
			Data    []struct {
				Last float64 `json:"last"`
			} `json:"data"`
		}
		if json.Unmarshal(msgBytes, &envelope) == nil &&
			envelope.Channel == "ticker" && len(envelope.Data) > 0 && envelope.Data[0].Last > 0 {
			m.Set(cex, envelope.Data[0].Last)
		}
	}
}

func (m *Manager) runBinanceFeed(ctx context.Context, cex string) error {
	ws, _, err := gorillaws.DefaultDialer.DialContext(ctx, "wss://stream.binance.com:9443/ws/btceur@miniTicker", nil)
	if err != nil {
		return err
	}
	defer ws.Close()
	fmt.Printf("[%sFeed] connected\n", cex)

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		_, msgBytes, err := ws.ReadMessage()
		if err != nil {
			return err
		}
		var msg struct {
			EventType string `json:"e"`
			Close     string `json:"c"`
		}
		if json.Unmarshal(msgBytes, &msg) == nil && msg.EventType == "24hrMiniTicker" {
			if p, err := strconv.ParseFloat(msg.Close, 64); err == nil && p > 0 {
				m.Set(cex, p)
			}
		}
	}
}

func (m *Manager) runBitstampFeed(ctx context.Context, cex string) error {
	ws, _, err := gorillaws.DefaultDialer.DialContext(ctx, "wss://ws.bitstamp.net", nil)
	if err != nil {
		return err
	}
	defer ws.Close()
	fmt.Printf("[%sFeed] connected\n", cex)

	sub := `{"event":"bts:subscribe","data":{"channel":"live_trades_btceur"}}`
	if err := ws.WriteMessage(gorillaws.TextMessage, []byte(sub)); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		_, msgBytes, err := ws.ReadMessage()
		if err != nil {
			return err
		}
		var msg struct {
			Event string `json:"event"`
			Data  struct {
				Price float64 `json:"price"`
			} `json:"data"`
		}
		if json.Unmarshal(msgBytes, &msg) == nil && msg.Event == "trade" && msg.Data.Price > 0 {
			m.Set(cex, msg.Data.Price)
		}
	}
}

func (m *Manager) runCoinbaseFeed(ctx context.Context, cex string) error {
	ws, _, err := gorillaws.DefaultDialer.DialContext(ctx, "wss://advanced-trade-ws.coinbase.com", nil)
	if err != nil {
		return err
	}
	defer ws.Close()
	fmt.Printf("[%sFeed] connected\n", cex)

	sub := `{"type":"subscribe","product_ids":["BTC-EUR"],"channel":"ticker"}`
	if err := ws.WriteMessage(gorillaws.TextMessage, []byte(sub)); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		_, msgBytes, err := ws.ReadMessage()
		if err != nil {
			return err
		}
		var msg struct {
			Channel string `json:"channel"`
			Events  []struct {
				Tickers []struct {
					Price string `json:"price"`
				} `json:"tickers"`
			} `json:"events"`
		}
		if json.Unmarshal(msgBytes, &msg) == nil && msg.Channel == "ticker" {
			for _, ev := range msg.Events {
				for _, t := range ev.Tickers {
					if p, err := strconv.ParseFloat(t.Price, 64); err == nil && p > 0 {
						m.Set(cex, p)
					}
				}
			}
		}
	}
}
