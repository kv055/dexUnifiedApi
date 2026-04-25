package paymentnotifications

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Type string

const (
	Incoming Type = "incoming"
	Outgoing Type = "outgoing"
)

type Notification struct {
	Amount          float64        `json:"amount"`
	Currency        string         `json:"currency"`
	Type            Type           `json:"type"`
	TransactionID   string         `json:"transaction_id"`
	DEX             string         `json:"dex,omitempty"`
	Connector       string         `json:"connector"`
	TransferMessage string         `json:"transfer_message,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
	Timestamp       time.Time      `json:"timestamp"`
}

type StoredNotification struct {
	ID         string       `json:"id"`
	ReceivedAt time.Time    `json:"received_at"`
	Event      Notification `json:"event"`
}

type Service struct {
	mu     sync.RWMutex
	recent []StoredNotification
	limit  int
}

func NewService(limit int) *Service {
	if limit <= 0 {
		limit = 250
	}
	return &Service{limit: limit}
}

func (s *Service) Add(event Notification) (StoredNotification, error) {
	if err := Validate(&event); err != nil {
		return StoredNotification{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	stored := StoredNotification{
		ID:         uuid.NewString(),
		ReceivedAt: time.Now().UTC(),
		Event:      event,
	}
	s.recent = append([]StoredNotification{stored}, s.recent...)
	if len(s.recent) > s.limit {
		s.recent = s.recent[:s.limit]
	}
	return stored, nil
}

func (s *Service) List(limit int) []StoredNotification {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 || limit > len(s.recent) {
		limit = len(s.recent)
	}
	out := make([]StoredNotification, limit)
	copy(out, s.recent[:limit])
	return out
}

func Validate(event *Notification) error {
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
	case Incoming, Outgoing:
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
