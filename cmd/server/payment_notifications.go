package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"dexunifiedapi/internal/paymentnotifications"
)

func handleInternalPaymentNotifications(w http.ResponseWriter, r *http.Request) {
	if !internalSecretAuthorized(r.Header.Get("X-Webhook-Secret"), paymentNotificationSecret) {
		http.Error(w, "unauthorized webhook secret", http.StatusUnauthorized)
		return
	}

	var event paymentnotifications.Notification
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxInternalBodyBytes)).Decode(&event); err != nil {
		http.Error(w, fmt.Sprintf("decode notification: %v", err), http.StatusBadRequest)
		return
	}
	if paymentNotifications == nil {
		paymentNotifications = paymentnotifications.NewService(250)
	}
	stored, err := paymentNotifications.Add(event)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

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
	if paymentNotifications == nil {
		paymentNotifications = paymentnotifications.NewService(250)
	}

	writeJSON(w, map[string]any{
		"notifications": paymentNotifications.List(limit),
	})
}
