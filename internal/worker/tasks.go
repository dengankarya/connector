package worker

// TaskExpirePayments is the periodic job that expires stale awaiting_payment transactions.
const TaskExpirePayments = "payment:jobs:expire_payments"

// TaskRetryWebhooks is the periodic job that replays failed webhook events.
const TaskRetryWebhooks = "payment:jobs:retry_webhooks"

// TaskSyncSettlement is the nightly job that reconciles settlement status and fee breakdowns from Xendit.
const TaskSyncSettlement = "payment:jobs:sync_settlement"

// TaskCancelExpiredOrder is the one-shot job that calls Tokokarya to cancel a specific order at a scheduled time.
const TaskCancelExpiredOrder = "payment:jobs:cancel_expired_order"
