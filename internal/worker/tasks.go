package worker

// TaskExpirePayments is the periodic job that expires stale awaiting_payment transactions.
const TaskExpirePayments = "payment:jobs:expire_payments"

// TaskRetryWebhooks is the periodic job that replays failed webhook events.
const TaskRetryWebhooks = "payment:jobs:retry_webhooks"

// TaskCancelExpiredOrder is the one-shot job that calls Tokokarya to cancel a specific order at a scheduled time.
const TaskCancelExpiredOrder = "payment:jobs:cancel_expired_order"

// TaskSyncXenditSettlements is the periodic job that checks and marks settled Xendit transactions.
const TaskSyncXenditSettlements = "payment:jobs:sync_xendit_settlements"
