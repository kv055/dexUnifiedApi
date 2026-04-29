package paymentnotifications

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Type identifies a type.
type Type string

const (
	// Incoming is the incoming value.
	Incoming Type = "incoming"
	// Outgoing is the outgoing value.
	Outgoing Type = "outgoing"
)

// Notification represents a notification.
type Notification struct {
	// Amount is the amount value for the notification.
	Amount float64 `json:"amount"`
	// Currency names the asset or network for the notification.
	Currency string `json:"currency"`
	// Type is the type value for the notification.
	Type Type `json:"type"`
	// TransactionID is the transaction id value for the notification.
	TransactionID string `json:"transaction_id"`
	// DEX is the dex value for the notification.
	DEX string `json:"dex,omitempty"`
	// Connector is the connector value for the notification.
	Connector string `json:"connector"`
	// TransferMessage carries a human-readable message for the notification.
	TransferMessage string `json:"transfer_message,omitempty"`
	// Metadata preserves additional source data for the notification.
	Metadata map[string]any `json:"metadata,omitempty"`
	// Timestamp records the relevant time for the notification.
	Timestamp time.Time `json:"timestamp"`
}

// StoredNotification represents a stored notification.
type StoredNotification struct {
	// ID identifies the stored notification.
	ID string `json:"id"`
	// ReceivedAt records the relevant time for the stored notification.
	ReceivedAt time.Time `json:"received_at"`
	// Event is the event value for the stored notification.
	Event Notification `json:"event"`
}

// Service represents a service.
type Service struct {
	mu     sync.RWMutex
	recent []StoredNotification
	limit  int
}

// NewService creates a service.
func NewService(limit int) *Service {
	if limit <= 0 {
		limit = 250
	}
	return &Service{limit: limit}
}

// Add validates and stores a payment notification in the recent-event buffer.
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

// List returns recent records, capped by the requested limit.
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

// Validate validates the provided value.
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
