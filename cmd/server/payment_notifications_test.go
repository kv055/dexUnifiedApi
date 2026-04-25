package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"dexunifiedapi/internal/paymentnotifications"
)

func TestHandleInternalPaymentNotificationsAcceptsValidPayload(t *testing.T) {
	oldService := paymentNotifications
	oldSecret := paymentNotificationSecret
	paymentNotifications = paymentnotifications.NewService(10)
	paymentNotificationSecret = "secret"
	defer func() {
		paymentNotifications = oldService
		paymentNotificationSecret = oldSecret
	}()

	payload := paymentnotifications.Notification{
		Amount:        12.5,
		Currency:      "eur",
		Type:          paymentnotifications.Incoming,
		TransactionID: "tx-1",
		DEX:           "Bisq",
		Connector:     "paypal",
		Timestamp:     time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC),
	}

	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/internal/payment_notifications", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Webhook-Secret", "secret")
	rr := httptest.NewRecorder()

	handleInternalPaymentNotifications(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", rr.Code)
	}
	if got := len(paymentNotifications.List(10)); got != 1 {
		t.Fatalf("expected 1 stored notification, got %d", got)
	}
}

func TestHandleInternalPaymentNotificationsRejectsInvalidPayload(t *testing.T) {
	oldSecret := paymentNotificationSecret
	paymentNotificationSecret = ""
	defer func() {
		paymentNotificationSecret = oldSecret
	}()

	req := httptest.NewRequest(http.MethodPost, "/internal/payment_notifications", bytes.NewBufferString(`{"amount":0}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handleInternalPaymentNotifications(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestHandlePaymentNotificationsReturnsRecentEntries(t *testing.T) {
	oldService := paymentNotifications
	paymentNotifications = paymentnotifications.NewService(10)
	_, err := paymentNotifications.Add(paymentnotifications.Notification{
		Amount:        5,
		Currency:      "EUR",
		Type:          paymentnotifications.Outgoing,
		TransactionID: "tx-2",
		Connector:     "wise",
		Timestamp:     time.Date(2026, 4, 18, 11, 59, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("seed notification: %v", err)
	}
	defer func() {
		paymentNotifications = oldService
	}()

	req := httptest.NewRequest(http.MethodGet, "/api/payment_notifications?limit=1", nil)
	rr := httptest.NewRecorder()

	handlePaymentNotifications(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if body := rr.Body.String(); body == "" {
		t.Fatal("expected response body")
	}
}
