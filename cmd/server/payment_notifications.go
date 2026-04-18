package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type paymentNotificationType string

const (
	paymentNotificationIncoming paymentNotificationType = "incoming"
	paymentNotificationOutgoing paymentNotificationType = "outgoing"
)

type paymentNotification struct {
	Amount          float64                 `json:"amount"`
	Currency        string                  `json:"currency"`
	Type            paymentNotificationType `json:"type"`
	TransactionID   string                  `json:"transaction_id"`
	DEX             string                  `json:"dex,omitempty"`
	Connector       string                  `json:"connector"`
	TransferMessage string                  `json:"transfer_message,omitempty"`
	Metadata        map[string]any          `json:"metadata,omitempty"`
	Timestamp       time.Time               `json:"timestamp"`
}

type storedPaymentNotification struct {
	ID         string              `json:"id"`
	ReceivedAt time.Time           `json:"received_at"`
	Event      paymentNotification `json:"event"`
}

type paymentNotificationHub struct {
	mu     sync.RWMutex
	recent []storedPaymentNotification
	limit  int
}

var paymentHub = &paymentNotificationHub{limit: 250}

func (h *paymentNotificationHub) add(event paymentNotification) storedPaymentNotification {
	h.mu.Lock()
	defer h.mu.Unlock()

	stored := storedPaymentNotification{
		ID:         uuid.NewString(),
		ReceivedAt: time.Now().UTC(),
		Event:      event,
	}
	h.recent = append([]storedPaymentNotification{stored}, h.recent...)
	if len(h.recent) > h.limit {
		h.recent = h.recent[:h.limit]
	}
	return stored
}

func (h *paymentNotificationHub) list(limit int) []storedPaymentNotification {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if limit <= 0 || limit > len(h.recent) {
		limit = len(h.recent)
	}
	out := make([]storedPaymentNotification, limit)
	copy(out, h.recent[:limit])
	return out
}

func handleInternalPaymentNotifications(w http.ResponseWriter, r *http.Request) {
	if paymentNotificationSecret != "" && r.Header.Get("X-Webhook-Secret") != paymentNotificationSecret {
		http.Error(w, "unauthorized webhook secret", http.StatusUnauthorized)
		return
	}

	var event paymentNotification
	if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
		http.Error(w, fmt.Sprintf("decode notification: %v", err), http.StatusBadRequest)
		return
	}
	if err := validatePaymentNotification(&event); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	stored := paymentHub.add(event)
	fmt.Printf("[PaymentNotifications] accepted %s %s %.2f %s via %s (tx=%s, dex=%s)\n",
		stored.Event.Type,
		stored.Event.TransactionID,
		stored.Event.Amount,
		stored.Event.Currency,
		stored.Event.Connector,
		stored.Event.TransactionID,
		stored.Event.DEX,
	)

	w.WriteHeader(http.StatusAccepted)
	writeJSON(w, map[string]any{
		"accepted":    true,
		"id":          stored.ID,
		"received_at": stored.ReceivedAt,
	})
}

func handlePaymentNotifications(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	writeJSON(w, map[string]any{
		"notifications": paymentHub.list(limit),
	})
}

func validatePaymentNotification(event *paymentNotification) error {
	if event.Amount <= 0 {
		return fmt.Errorf("amount must be greater than zero")
	}

	event.Currency = strings.ToUpper(strings.TrimSpace(event.Currency))
	if event.Currency == "" {
		return fmt.Errorf("currency is required")
	}

	event.TransactionID = strings.TrimSpace(event.TransactionID)
	if event.TransactionID == "" {
		return fmt.Errorf("transaction_id is required")
	}

	event.Connector = strings.TrimSpace(event.Connector)
	if event.Connector == "" {
		return fmt.Errorf("connector is required")
	}

	event.DEX = strings.TrimSpace(event.DEX)
	switch event.Type {
	case paymentNotificationIncoming, paymentNotificationOutgoing:
	default:
		return fmt.Errorf("type must be one of incoming or outgoing")
	}

	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	} else {
		event.Timestamp = event.Timestamp.UTC()
	}

	if event.Metadata == nil {
		event.Metadata = map[string]any{}
	}
	return nil
}
