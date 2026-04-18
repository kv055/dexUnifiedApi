# dexUnifiedApi

## Payment Notifications

`dexUnifiedApi` now exposes a webhook receiver for fiat payment notifications.

- `POST /internal/payment_notifications`
- Optional auth via `PAYMENT_NOTIFICATIONS_SHARED_SECRET` and the `X-Webhook-Secret` header
- Accepted payload fields: `amount`, `currency`, `type`, `transaction_id`, `dex`, `connector`, `transfer_message`, `metadata`, `timestamp`
- Recent accepted notifications are available at `GET /api/payment_notifications?limit=50`

This gives the connectors and the fiat controller a stable contract now, while subscriber fan-out can be layered onto the in-memory hub later.
