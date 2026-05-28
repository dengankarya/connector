package worker

// TaskExpirePayments is the periodic job that expires stale awaiting_payment transactions.
const TaskExpirePayments = "payment:jobs:expire_payments"

// TaskRetryWebhooks is the periodic job that replays failed webhook events.
const TaskRetryWebhooks = "payment:jobs:retry_webhooks"

// TaskSyncSettlement is the nightly job that reconciles settlement status and fee breakdowns from Xendit.
const TaskSyncSettlement = "payment:jobs:sync_settlement"
