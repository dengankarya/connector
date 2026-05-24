package worker

// Task type names — must match between enqueuer and handler.
const TaskXenplatformAccountUpdated = "xenplatform:account_updated"

// XenplatformAccountUpdatedPayload is the task payload stored in Redis.
// It carries the raw Xendit webhook event so the handler can process it independently.
type XenplatformAccountUpdatedPayload struct {
	Event      string         `json:"event"`
	BusinessID string         `json:"business_id"`
	Data       map[string]any `json:"data"`
}

// ── Payment module job task names ───────────────────────────────────────────

// TaskExpirePayments is the periodic job that expires stale awaiting_payment transactions.
const TaskExpirePayments = "payment:jobs:expire_payments"

// TaskRetryWebhooks is the periodic job that replays failed webhook events.
const TaskRetryWebhooks = "payment:jobs:retry_webhooks"
