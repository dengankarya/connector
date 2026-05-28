# Payment Module

This document describes the design, data flow, and operational details of the payment module inside the connector service. It is the reference for anyone adding features, debugging production issues, or onboarding to the codebase.

---

## Table of Contents

1. [Overview](#1-overview)
2. [Package Structure](#2-package-structure)
3. [Payment Transaction Lifecycle](#3-payment-transaction-lifecycle)
4. [Webhook Processing Pipeline](#4-webhook-processing-pipeline)
5. [Double-Entry Ledger](#5-double-entry-ledger)
6. [Payout System](#6-payout-system)
7. [XenPlatform Sub-Account Routing (`for-user-id`)](#7-xenplatform-sub-account-routing-for-user-id)
8. [Manual Payments](#8-manual-payments)
9. [Webhook Replay & Retry](#9-webhook-replay--retry)
10. [Background Jobs](#10-background-jobs)
11. [Settlement Sync](#11-settlement-sync)
12. [API Reference](#12-api-reference)
13. [Security Design](#13-security-design)
14. [Observability & Logging](#14-observability--logging)
15. [Database Schema](#15-database-schema)
16. [Adding a New Payment Provider](#16-adding-a-new-payment-provider)
17. [Configuration](#17-configuration)
18. [Running & Migrations](#18-running--migrations)
19. [Failure Scenarios](#19-failure-scenarios)

---

## 1. Overview

The payment module sits between **Tokokarya** (the ecommerce platform) and **Xendit** (the payment gateway). It owns the full lifecycle of a payment: from session creation through webhook processing, ledger bookkeeping, and merchant payout.

```
Tokokarya API
    │
    ▼ POST /api/payments
┌────────────────────┐
│   Payment Module   │  ← this service
│                    │
│  ┌──────────────┐  │
│  │  Transaction │  │
│  │     DB       │  │
│  └──────────────┘  │
│  ┌──────────────┐  │
│  │    Ledger    │  │
│  │     DB       │  │
│  └──────────────┘  │
└────────┬───────────┘
         │
         ▼ HTTPS
      Xendit API
         │
         ▼ webhook callback
POST /webhook/xendit
```

**Core guarantees:**
- Every payment event is stored before any processing begins.
- All mutations for a single webhook (status update + ledger write) happen in one atomic DB transaction.
- The ledger is append-only and immutable.
- Every operation is idempotent — safe to retry at any layer.

---

## 2. Package Structure

```
internal/payment/
├── domain/           Pure entities and business rules. No infra imports.
│   ├── transaction.go  PaymentTransaction entity + state machine + IsValid()
│   ├── event.go        WebhookEvent entity + processing states
│   ├── ledger.go       LedgerEntry, LedgerJournal, double-entry validation
│   ├── payout.go       Payout entity
│   ├── settlement.go   MerchantSettlementSnapshot entity
│   ├── balance.go      Balance struct (settled + pending breakdown)
│   └── errors.go       All typed domain errors
│
├── repository/       PostgreSQL persistence (pgx/v5).
│   ├── db.go           DBTX interface, TxRunner, context-based tx propagation
│   ├── transaction.go  PaymentTransaction CRUD + FOR UPDATE queries + keyset pagination
│   ├── event.go        WebhookEvent CRUD + status management
│   ├── ledger.go       Append-only ledger writes + balance queries
│   ├── payout.go       Payout CRUD
│   ├── settlement.go   MerchantSettlementSnapshot upsert + lookup
│   ├── idempotency.go  Idempotency key management
│   └── helpers.go      Shared scan/marshal helpers
│
├── provider/         Payment gateway abstraction.
│   ├── interface.go    PaymentProvider + TransactionSyncer interfaces
│   └── xendit/
│       ├── provider.go  Xendit HTTP client (implements both interfaces)
│       ├── mapper.go    Xendit DTO ↔ domain type conversion
│       └── signature.go Webhook token validation
│
├── ledger/
│   └── service.go    Double-entry journal writer (RecordPayment, RecordSettlement, RecordRefund, RecordPayout)
│
├── webhook/
│   ├── processor.go  Core orchestration: ingest + process (the most critical file)
│   ├── handler.go    Asynq task handler wiring
│   ├── replay.go     Admin replay/retry service
│   └── parse.go      Raw payload field extraction
│
├── service/
│   ├── payment.go    CreatePayment, GetPayment, RefreshPayment, ListTransactions, GetMerchantBalance
│   └── payout.go     CreatePayout, DispatchPayout use cases
│
├── jobs/
│   ├── expire_payments.go    Periodic job: expire stale invoices
│   ├── retry_webhooks.go     Periodic job: replay failed webhook events
│   └── sync_settlement.go    Nightly job: reconcile Xendit transactions + upsert snapshots
│
├── controller.go     HTTP handlers + route registration
├── model.go          XenPlatform account models (legacy)
└── service.go        XenPlatform account service (legacy)
```

The **domain** package has no imports from within this project — it is the innermost layer and can be read in isolation to understand all business rules.

---

## 3. Payment Transaction Lifecycle

All monetary amounts are integers in the **smallest currency unit** (IDR has no decimal subdivision — an amount of `100000` means Rp 100,000).

### State Machine

```
                  ┌──────────┐
                  │ pending  │  ← created, before session is created at Xendit
                  └────┬─────┘
                       │ session created (POST /sessions)
                       ▼
             ┌──────────────────┐
             │ awaiting_payment │  ← customer on hosted checkout page
             └────┬──────┬──────┘
                  │      │
  payment.capture │      │ payment.failure
                  │      │  (failure_code=PAYMENT_REQUEST_EXPIRED → expired,
                  ▼      │   other → failed)
              ┌──────┐   ▼
              │ paid │  ┌─────────┐  ┌────────┐
              └──┬───┘  │ expired │  │ failed │  (terminal)
                 │      └─────────┘  └────────┘
                 │ refund initiated
                 ▼
           ┌───────────┐
           │ refunding │
           └─────┬─────┘
                 │ refund confirmed
                 ▼
           ┌──────────┐
           │ refunded │  (terminal)
           └──────────┘
```

`settled` is reachable from `paid`. The nightly settlement sync job transitions paid transactions to `settled` once Xendit confirms `settlement_status = SETTLED` via `GET /transactions`.

Terminal states (`expired`, `failed`, `refunded`, `voided`) accept no further transitions. Attempting one returns `ErrInvalidStatusTransition`.

Returning `ErrAlreadyInState` means the event was already applied — callers treat this as a no-op, not a failure. This is how idempotency is enforced at the domain level.

`PaymentStatus.IsValid()` returns `true` for any status in the state machine. The list-transactions API uses this to validate the `status` query parameter and reject unknown values with a 400.

### Amount Fields

| Field                  | Meaning                                        | When populated |
|------------------------|------------------------------------------------|----------------|
| `amount`               | Gross amount paid by the customer              | At creation |
| `platform_fee`         | Fee kept by the platform                       | At creation |
| `merchant_amount`      | `amount - platform_fee` (net to merchant)      | At creation |
| `xendit_fee`           | Xendit's processing fee                        | After settlement sync |
| `vat`                  | VAT on the Xendit fee                          | After settlement sync |
| `xendit_withholding_tax` | Xendit's withholding tax                     | After settlement sync |
| `third_party_wht`      | Third-party withholding tax                    | After settlement sync |

The DB enforces `merchant_amount + platform_fee = amount` as a check constraint. The domain `Validate()` method checks this before writing any ledger entry.

Fee fields (`xendit_fee`, `vat`, etc.) are backfilled by the nightly settlement sync job — they are always `0` until the job runs and matches the transaction to a Xendit `GET /transactions` record.

---

## 4. Webhook Processing Pipeline

This is the most critical path in the system. Every step is designed to be safe under concurrent execution and safe to retry.

### Step-by-step flow

```
POST /webhook/xendit
        │
        ▼
1.  Validate x-callback-token (constant-time comparison)
    │  Invalid → log + return 200 silently (don't leak info to attacker)
    │
        ▼
2.  Parse payload to extract event_type + provider_invoice_id
        │
        ▼
3.  INSERT INTO payment_webhook_events
    ON CONFLICT (provider, provider_event_id) DO NOTHING
    │  RowsAffected = 0 → duplicate; return 200 immediately
    │
        ▼
4.  Return 200 OK immediately ← HTTP handler exits here
        │
        ▼ (async, asynq worker)
5.  SELECT ... FOR UPDATE  ← lock webhook event row
    │  Prevents two workers processing the same event
    │
        ▼
6.  Idempotency check
    │  status = 'processed' → return nil (safe no-op)
    │
        ▼
7.  Mark status = 'processing', increment processing_attempts
        │
        ▼
8.  SELECT ... FOR UPDATE  ← lock payment_transaction row
    │  Prevents race with concurrent webhooks for the same invoice
    │
        ▼
9.  Validate state transition (domain state machine)
        │
        ▼
10. Update payment_transaction (status, paid_at, payment_method, etc.)
    + Create ledger entries
    │  All inside the same DB transaction started at step 5
    │
        ▼
11. Mark webhook event status = 'processed', link transaction_id
        │
        ▼
12. COMMIT  ← single atomic commit covering steps 5-11
```

**Why two `FOR UPDATE` locks?**

The webhook event lock (step 5) prevents two asynq workers from processing the same task in parallel. The transaction lock (step 8) prevents a second webhook (e.g., a `payment.capture` retransmission arriving while the first is still processing) from reading stale state.

**Why return 200 immediately at step 4?**

Xendit expects a 200 within a few seconds. Heavy processing (DB transaction, potential retries) happens asynchronously. If the DB is slow, the webhook is still safely stored and will be processed — or retried by the job.

### Supported event types

Xendit fires these events after a **Payment Session** (`POST /sessions`) is completed or fails:

| Xendit event                  | Transition                        | Ledger journal written     | Notes |
|-------------------------------|-----------------------------------|----------------------------|-------|
| `payment_session.completed`   | `awaiting_payment → paid`         | Payment journal (escrow DR, merchant_payable CR, platform_fee CR) | Primary Sessions API event; `payment_id` stored as `provider_payment_id` |
| `payment_session.expired`     | `awaiting_payment → expired`      | None                       | Session timed out before payment |
| `payment_session.failed`      | `awaiting_payment → failed`       | None                       | `failure_code` logged |
| `payment.capture`             | `awaiting_payment → paid`         | Same as above              | Payment Request API — kept for compatibility |
| `payment.authorization`       | None                              | None                       | No-op for AUTOMATIC capture |
| `payment.failure`             | `→ expired` or `→ failed`         | None                       | Payment Request API; `PAYMENT_REQUEST_EXPIRED` → expired, other → failed |
| Anything else                 | No transition                     | No entries; event marked processed | |

### Account update events

Xendit sends account lifecycle events (e.g. `account.updated`, `kyc.updated`) alongside payment events on the same webhook endpoint. The controller detects these by the presence of a `business_id` field in the payload and routes them to the `XenplatformAccountUpdated` asynq task for separate processing. Payment webhook events (no `business_id`) proceed through the standard processing pipeline above.

---

## 5. Double-Entry Ledger

The ledger is **append-only** and **immutable**. Rows in `payment_ledger_entries` are never updated or deleted.

Every financial event creates a balanced **journal**: the sum of all debit entries equals the sum of all credit entries. An imbalanced journal is a programming bug — `LedgerJournal.Validate()` panics before reaching the DB.

### Account types

| Account               | What it represents                                    |
|-----------------------|-------------------------------------------------------|
| `escrow`              | Gross funds held by the platform on behalf of merchants |
| `merchant_payable`    | Net amount owed to a merchant (liability)             |
| `platform_fee`        | Revenue earned by the platform                        |
| `payout`              | Disbursements made to merchants                       |
| `refund`              | Funds returned to customers                           |

### Journals

**Payment captured** (`payment.capture`):
```
DR  escrow                amount           (asset: platform holds funds)
CR  merchant_payable      merchant_amount  (liability: owed to merchant)
CR  platform_fee          platform_fee     (revenue: platform earns)
```

**Settlement confirmed** (future — triggered manually or via future webhook):
```
DR  merchant_payable      merchant_amount  (informational audit pair only)
CR  merchant_payable      merchant_amount  (no net balance change)
```

**Refund issued**:
```
DR  refund                refund_amount    (expense: funds returned)
CR  escrow                refund_amount    (asset reduced)
```

**Payout disbursed**:
```
DR  merchant_payable      amount           (liability reduced)
CR  payout                amount           (disbursement recorded)
```

### Idempotency

Each ledger entry has a `reference_id` — a unique string per financial event. There is a `UNIQUE` index on `(tenant_id, reference_id)`. If the same journal is written twice (e.g., on replay), `ON CONFLICT DO NOTHING` fires and the write is silently skipped. This is safe: the first write committed correctly, so the duplicate has no effect.

Reference ID format: `txn:{transaction_id}:{event}:{account}`, e.g. `txn:abc-123:paid:escrow`.

### Querying balances

```sql
-- How much does the platform owe merchant X?
SELECT COALESCE(SUM(CASE direction WHEN 'credit' THEN amount ELSE -amount END), 0)
FROM payment_ledger_entries
WHERE tenant_id = $1 AND account_type = 'merchant_payable';
```

Or use `LedgerRepository.SumByAccountType()` in code.

---

## 6. Payout System

A payout moves money from `merchant_payable` to `payout` in the ledger, and dispatches a disbursement request to the provider.

### Lifecycle

```
pending → processing → completed
                    ↘ failed (retry if retry_count < max_retries)
```

`DispatchPayout` runs inside a `TxRunner.RunInTx`:
1. `SELECT ... FOR UPDATE` on the payout row
2. Mark `processing`
3. Call `provider.CreatePayout()`
4. On success: mark `completed`, write ledger entries
5. On failure: increment `retry_count`; if `retry_count >= max_retries` mark `failed`; else reset to `pending`

---

## 7. XenPlatform Sub-Account Routing (`for-user-id`)

Tokokarya uses **Xendit XenPlatform** to operate as a payment facilitator. Each merchant has their own Xendit sub-account (a business ID issued by Xendit). The platform's main API key is used for all API calls, but individual operations are scoped to a merchant's sub-account by sending the `for-user-id: <xendit_account_id>` request header.

### What `for-user-id` does

| Operation | Effect when `for-user-id` is set |
|-----------|----------------------------------|
| Create session (`POST /sessions`) | Session is owned by the sub-account. Customer payments go into the sub-account's balance, not the platform's. |
| Get session (`GET /sessions/:id`) | Reads from the sub-account's session namespace. |
| Cancel session (`POST /sessions/:id/cancel`) | Cancels within the sub-account. |
| Create refund | Refund is charged against the sub-account's balance. |
| Create payout (`POST /disbursements`) | Disbursement is sent from the sub-account's balance. |
| Transfer (`POST /transfers`) | **Not used** — transfers are a platform-level operation that routes funds between the main account and a sub-account. The `for-user-id` header is intentionally omitted here. |

### Where the ID is stored

The merchant's Xendit sub-account ID is stored in two places:

- **`payment_transactions.xendit_account_id`** — set at payment creation, read by the webhook processor to trigger the post-payment transfer.
- **`payment_payouts.for_user_id`** — set at payout creation, read by `DispatchPayout` so background jobs can scope the disbursement to the correct sub-account without an additional lookup.

### Payment flow with `for-user-id`

```
1. POST /api/payments  { "for_user_id": "xnd_acct_merchant_123", ... }
         │
         ▼
2. POST /sessions → Xendit API
   Header: for-user-id: xnd_acct_merchant_123
   Response: payment_session_id (ps-xxx) + payment_link_url (stored on the transaction)
         │
         ▼ (customer completes payment on hosted checkout page)
3. payment.capture webhook received
   data.payment_session_id = ps-xxx
   data.payment_id = py-xxx
         │
         ▼
4. DB transaction: mark paid + write ledger entries (escrow/merchant_payable/platform_fee)
         │
         ▼ (to be implemented — see TODO in processor.go)
5. POST /transfers → Xendit API  (no for-user-id header)
   { "reference": "txn:<id>:transfer",
     "amount": merchant_amount,
     "destination_user_id": "xnd_acct_merchant_123" }
   → merchant_amount moved to sub-account balance
```

The transfer (step 5) will be triggered after payment is confirmed. The `provider.Transfer` method and `TransferRequest` type are already defined; the trigger will be added to `handlePaid` or `handleSettled` when the settlement flow is finalised.

### When `for-user-id` is not set

If `for_user_id` is omitted from the create-payment request, all Xendit API calls operate on the **platform's main account**. The transfer step after payment is also skipped (`xendit_account_id` is empty). This is valid for platform-owned payments where no merchant sub-account routing is needed.

---

## 8. Manual Payments

Manual payments cover any payment collected outside Xendit — cash, direct bank transfers, or any other offline method. The connector records the transaction so it participates in the same ledger, reporting, and payout flow as normal Xendit payments.

### How it works

Manual payments reuse the existing Xendit webhook pipeline. The connector generates a synthetic `provider_invoice_id` (prefixed `manual-`) and stores the transaction with `provider="xendit"`. When Tokokarya confirms the payment, it calls the existing `POST /webhook/xendit` endpoint with a Xendit-shaped payload — the processor finds the transaction by that ID and processes it identically to a real Xendit payment.

```
1. Tokokarya  →  POST /api/payments/manual
                 { order_number, amount, payment_method: "CASH", ... }

2. Connector  →  generates provider_invoice_id = "manual-<uuid>"
                 stores transaction (provider="xendit", status=awaiting_payment)
                 returns { provider_invoice_id: "manual-<uuid>", ... }

3. Customer pays offline

4. Tokokarya admin confirms payment

5. Tokokarya  →  POST /webhook/xendit
                 x-callback-token: <XENDIT_WEBHOOK_TOKEN>
                 { "event": "payment_session.completed",
                   "data": { "payment_session_id": "manual-<uuid>", ... } }

6. Connector  →  finds transaction by provider_invoice_id = "manual-<uuid>"
                 transitions awaiting_payment → paid
                 writes ledger entries (same double-entry journal as Xendit)
                 forwards event to Tokokarya
```

### What Tokokarya must send in step 5

```http
POST /webhook/xendit
x-callback-token: <XENDIT_WEBHOOK_TOKEN>
Content-Type: application/json

{
  "event": "payment_session.completed",
  "business_id": "",
  "created": "2026-05-24T10:00:00Z",
  "data": {
    "payment_session_id": "manual-550e8400-e29b-41d4-a716-446655440000",
    "payment_id": "",
    "payment_request_id": "",
    "reference_id": "order-ORD-2026-001-attempt-1",
    "status": "SUCCEEDED",
    "channel_code": "CASH",
    "currency": "IDR",
    "request_amount": 100000,
    "created": "2026-05-24T10:00:00Z",
    "updated": "2026-05-24T10:00:00Z"
  }
}
```

| Field | Value | Notes |
|---|---|---|
| `event` | `"payment_session.completed"` | Use this for a successful payment confirmation |
| `data.payment_session_id` | The `provider_invoice_id` returned by `POST /api/payments/manual` | **Required** — this is how the processor finds the transaction |
| `data.channel_code` | e.g. `"CASH"`, `"BANK_TRANSFER"`, `"BCA"` | Stored as `payment_method` on the transaction |
| `data.currency` | e.g. `"IDR"` | Should match the transaction currency |
| `data.request_amount` | The amount paid | Informational only; the ledger uses the amount from the stored transaction |
| `data.updated` | Timestamp of confirmation | Stored as `paid_at` on the transaction |
| `business_id` | `""` (empty string) | Must be empty or absent — non-empty triggers the XenPlatform account update path instead |

To mark a manual payment as **failed** (e.g., customer bounced a cheque), send:

```json
{
  "event": "payment_session.failed",
  "business_id": "",
  "created": "2026-05-24T10:00:00Z",
  "data": {
    "payment_session_id": "manual-550e8400-e29b-41d4-a716-446655440000",
    "status": "FAILED",
    "failure_code": "PAYMENT_FAILED",
    "currency": "IDR",
    "request_amount": 100000,
    "created": "2026-05-24T10:00:00Z",
    "updated": "2026-05-24T10:00:00Z"
  }
}
```

### Differences from Xendit payments

| | Xendit payment | Manual payment |
|---|---|---|
| `provider_invoice_id` | Xendit `ps-xxx` | `manual-<uuid>` generated locally |
| `checkout_url` | Xendit hosted checkout page URL | Empty — no checkout page |
| Creation flow | Connector calls Xendit API | No external API call |
| Confirmation | Xendit sends the webhook | Tokokarya sends the webhook |
| Processing | Identical | Identical |
| Ledger | Identical | Identical |

---

## 9. Webhook Replay & Retry

### Automatic retry

asynq retries failed tasks with exponential backoff (5 attempts by default, configured in `webhook/handler.go:NewTask`):

| Attempt | Approx delay |
|---------|-------------|
| 1       | immediate   |
| 2       | ~30s        |
| 3       | ~5m         |
| 4       | ~30m        |
| 5       | ~3h         |

After 5 failures, the event's `processing_status` becomes `failed` in the DB.

### Job-based retry

The `retry_webhooks` background job runs every 10 minutes and calls `ReplayService.ReplayFailed()`. It re-enqueues all events in `failed` status (up to 50 at a time). This catches events that asynq dropped or that failed before being enqueued.

### Manual replay via API

```http
POST /api/payments/webhooks/{event_id}/replay
Content-Type: application/json
X-API-KEY: <key>

{ "force": false }
```

| `force` | Behaviour |
|---------|-----------|
| `false` | Returns `409 Conflict` if the event is already `processed`. Safe default. |
| `true`  | Resets the event to `received` and re-enqueues regardless of current status. Use when you need to re-apply a processed event after a bug fix. |

**Replay is always safe because:**
- The processor does `SELECT FOR UPDATE` on the event before acting.
- The transaction state machine rejects invalid transitions.
- Ledger entries have unique `reference_id` constraints — duplicates are silently ignored.

### Dead-lettered events

If an event fails all asynq retries AND the retry job cannot re-enqueue it, call `ReplayService.MarkDeadLettered()`. Dead-lettered events require explicit `force=true` replay.

---

## 10. Background Jobs

Jobs are registered as asynq periodic tasks via the scheduler in `http/main.go`. They run inside the same process as the HTTP server.

| Task name                        | Schedule         | What it does |
|----------------------------------|------------------|--------------|
| `payment:jobs:expire_payments`   | every 5m         | Finds `awaiting_payment` transactions past `expires_at`, transitions them to `expired`. Uses `SKIP LOCKED` so multiple instances don't contend. |
| `payment:jobs:retry_webhooks`    | every 10m        | Re-enqueues up to 50 `failed` webhook events. |
| `payment:jobs:sync_settlement`   | daily at 1 AM WIB (18:00 UTC) | Calls Xendit `GET /transactions`, reconciles fee breakdowns, transitions `paid → settled`. See [Section 11](#11-settlement-sync). |

All jobs are idempotent. Running them multiple times in quick succession is safe.

---

## 11. Settlement Sync

### Why it exists

Xendit does not send a webhook when a payment settles. The only way to know that funds have moved from "collected" to "settled" (and to get the exact fee breakdown) is to poll `GET /transactions` on the Xendit API.

The settlement sync job runs **nightly at 1 AM WIB (18:00 UTC)** and does two things:
1. **Reconciles individual transactions** — for every `paid` transaction where Xendit now reports `settlement_status = SETTLED`, it transitions the transaction to `settled` and backfills the fee breakdown (`xendit_fee`, `vat`, `xendit_withholding_tax`, `third_party_wht`, `estimated_settlement_time`).
2. **Updates the merchant snapshot** — re-aggregates all `paid` transactions for the tenant into `merchant_settlement_snapshots`, giving an instant read on pending balance without scanning the full transaction table on every balance request.

### How it works

```
SyncSettlementJob.Run()
    │
    ├─ ListDistinctXenditAccounts()   ← find all tenants with a xendit_account_id
    │
    └─ for each (tenant_id, xendit_account_id):
           syncTenant(ctx, tenant_id, xendit_account_id)
                │
                ├─ Determine since window
                │   snapshot exists → use snapshot.last_synced_at (avoid refetching all history)
                │   no snapshot yet → use 30 days ago as a safe bootstrap window
                │
                ├─ Paginate Xendit GET /transactions?types=PAYMENT&cashflow=MONEY_IN
                │   Filters: after_created_at=since, limit=100, after_id=<cursor>
                │   Continue until HasMore=false
                │
                ├─ for each ProviderTransaction:
                │       match by product_data.payment_session_id → provider_invoice_id
                │       if settlement_status = SETTLED:
                │           backfill fee fields (xendit_fee, vat, withholding, etc.)
                │           transition paid → settled (skip if already settled or any error)
                │           update row (optimistic lock — ErrVersionConflict skipped, retried next night)
                │
                ├─ SumPendingSettlement(tenant_id)
                │   aggregate: SUM(merchant_amount), SUM(platform_fee), SUM(xendit_fee),
                │               SUM(vat), SUM(xendit_withholding_tax + third_party_wht)
                │   WHERE status = 'paid'   ← paid-but-not-yet-settled
                │
                └─ Upsert merchant_settlement_snapshots
                    ON CONFLICT (tenant_id) DO UPDATE SET
                        pending_balance, pending_platform_fee, pending_xendit_fee,
                        pending_vat, pending_withholding, last_synced_at, updated_at
```

### TransactionSyncer interface

Only the `SyncSettlementJob` depends on Xendit's `GET /transactions` API. This is abstracted behind a separate `TransactionSyncer` interface (in `provider/interface.go`) so the sync job is not coupled to the full `PaymentProvider` interface and a different provider can implement it independently:

```go
type TransactionSyncer interface {
    ListTransactions(ctx context.Context, req ListTransactionsRequest) (*ListTransactionsResult, error)
}
```

The Xendit `Provider` implements both `PaymentProvider` and `TransactionSyncer`.

### merchant_settlement_snapshots

One row per tenant. Updated atomically by the sync job via `ON CONFLICT (tenant_id) DO UPDATE`. This is a **cache / summary**, not a ledger. The authoritative fee breakdown lives on each `payment_transactions` row. If the snapshot is wrong, rerunning the job recalculates it.

| Column               | Meaning |
|----------------------|---------|
| `pending_balance`    | SUM of `merchant_amount` for `paid` (not yet settled) transactions |
| `pending_platform_fee` | SUM of `platform_fee` for the same transactions |
| `pending_xendit_fee` | SUM of `xendit_fee` (backfilled from Xendit) |
| `pending_vat`        | SUM of `vat` |
| `pending_withholding`| SUM of `xendit_withholding_tax + third_party_wht` |
| `last_synced_at`     | When the job last ran for this tenant |

### What the balance endpoint returns

`GET /api/payments/balance` (see [Section 12](#12-api-reference)) combines:
- **settled_amount** — live balance from Xendit `GET /balance` (real-time API call)
- **pending_*** fields — read from `merchant_settlement_snapshots` (snapshot from last nightly run)
- **last_synced_at** — when the snapshot was last refreshed; `null` means the job has never run for this tenant

### Failure handling

| Scenario | Behaviour |
|---|---|
| Transaction already settled by a concurrent webhook | `ErrVersionConflict` → skip that transaction; the row is already correct |
| Xendit API error during pagination | Job logs error, stops the tenant sync, continues with next tenant |
| Snapshot upsert fails | Logged at ERROR; snapshot is stale but transaction rows are already updated |
| Job crashes mid-run | Safe to restart; `since` is read from the snapshot so the next run re-scans from the same window |

---

## 12. API Reference

All authenticated endpoints require the `X-API-KEY` header. Multi-tenant endpoints require `X-Tenant-ID` (integer).

### Endpoint summary

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/payments/` | Create Xendit payment (returns checkout URL) |
| `POST` | `/api/payments/manual` | Create manual payment (no external call) |
| `GET`  | `/api/payments/` | List transactions (cursor paginated, filterable) |
| `GET`  | `/api/payments/:id` | Get single transaction |
| `POST` | `/api/payments/:id/refresh` | Refresh status from Xendit for one transaction |
| `GET`  | `/api/payments/balance` | Merchant balance (settled + pending breakdown) |
| `POST` | `/api/payments/webhooks/:event_id/replay` | Replay a stored webhook event |
| `POST` | `/webhook/xendit` | Xendit webhook callback (public, no auth) |

### Create payment (Xendit)

```http
POST /api/payments/
X-API-KEY: <key>
X-Tenant-ID: 42
Content-Type: application/json

{
  "order_number": "ORD-2025-001",
  "idempotency_key": "order-ORD-2025-001-attempt-1",
  "amount": 100000,
  "currency": "IDR",
  "platform_fee": 2000,
  "description": "Order #ORD-2025-001",
  "customer_email": "buyer@example.com",
  "for_user_id": "xnd_acct_merchant_123",
  "expires_at": "2025-06-01T10:00:00Z",
  "allowed_payment_channels": ["BCA", "QRIS", "OVO"],
  "success_return_url": "https://tokokarya.com/order/ORD-2025-001/success",
  "cancel_return_url": "https://tokokarya.com/order/ORD-2025-001/cancel",
  "metadata": {}
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `order_number` | Yes | Tokokarya order identifier (string) |
| `idempotency_key` | Yes | Unique key per payment attempt; safe to retry |
| `amount` | Yes | Gross amount in IDR (integer, smallest unit) |
| `currency` | Yes | e.g. `"IDR"` |
| `platform_fee` | No | Defaults to 0. `merchant_amount = amount - platform_fee` |
| `for_user_id` | No | Merchant's Xendit sub-account ID; scopes the session to their account |
| `allowed_payment_channels` | No | Restrict which channels appear on the checkout page |
| `success_return_url` | No | Redirect URL after successful payment |
| `cancel_return_url` | No | Redirect URL if customer cancels |
| `customer_email` | No | Pre-fills the checkout form |
| `expires_at` | No | Session expiry (minimum 10 minutes from now; Xendit default is 30 min) |
| `description` | No | Shown on the Xendit checkout page |
| `metadata` | No | Arbitrary key-value pairs stored on the transaction |

Response `201 Created`:
```json
{
  "status": "Created",
  "data": {
    "id": "uuid",
    "order_number": "ORD-2025-001",
    "status": "awaiting_payment",
    "provider_invoice_id": "ps-661f87c614802d6c402cd82d",
    "checkout_url": "https://checkout.xendit.co/pay/ps-661f87c614802d6c402cd82d",
    "amount": 100000,
    "merchant_amount": 98000,
    "platform_fee": 2000,
    "expires_at": "2025-06-01T10:00:00Z"
  }
}
```

- `provider_invoice_id` is the Xendit `payment_session_id` (`ps-xxx`).
- `checkout_url` is the `payment_link_url` from Xendit — redirect the customer here to complete payment.
- Calling with the same `idempotency_key` returns the existing transaction without hitting Xendit again.

### Create manual payment

For payments collected outside Xendit. See [Section 8](#8-manual-payments) for the full flow.

```http
POST /api/payments/manual
X-API-KEY: <key>
X-Tenant-ID: 42
Content-Type: application/json

{
  "order_number": "ORD-2026-001",
  "idempotency_key": "order-ORD-2026-001-manual-1",
  "amount": 100000,
  "currency": "IDR",
  "platform_fee": 2000,
  "payment_method": "CASH",
  "payment_channel": "CASH",
  "description": "Cash payment for Order #ORD-2026-001",
  "metadata": {}
}
```

| Field | Required | Description |
|---|---|---|
| `order_number` | Yes | Tokokarya order identifier |
| `idempotency_key` | Yes | Unique key per payment attempt; safe to retry |
| `amount` | Yes | Gross amount in IDR (integer, smallest unit) |
| `currency` | Yes | e.g. `"IDR"` |
| `platform_fee` | No | Defaults to 0 |
| `payment_method` | No | e.g. `"CASH"`, `"BANK_TRANSFER"` |
| `payment_channel` | No | e.g. bank name |
| `description` | No | Free-text note |
| `metadata` | No | Arbitrary key-value pairs |

Response `201 Created`:
```json
{
  "status": "Created",
  "data": {
    "id": "uuid",
    "order_number": "ORD-2026-001",
    "status": "awaiting_payment",
    "provider": "xendit",
    "provider_invoice_id": "manual-550e8400-e29b-41d4-a716-446655440000",
    "checkout_url": "",
    "amount": 100000,
    "merchant_amount": 98000,
    "platform_fee": 2000,
    "payment_method": "CASH"
  }
}
```

**Save the `provider_invoice_id`** — Tokokarya must include it as `data.payment_session_id` when sending the confirmation webhook to `POST /webhook/xendit`.

Returns `409 Conflict` if an `idempotency_key` collision occurs.

### List transactions

```http
GET /api/payments/?limit=20&cursor=<token>&status=paid,settled&date_from=2026-05-01&date_to=2026-05-31
X-API-KEY: <key>
X-Tenant-ID: 42
```

| Query param | Default | Description |
|---|---|---|
| `limit` | `20` | Page size (max 100) |
| `cursor` | _(empty)_ | Opaque token from the previous response's `next_cursor` |
| `status` | _(all)_ | Comma-separated filter; valid values: `pending`, `awaiting_payment`, `paid`, `settled`, `refunding`, `refunded`, `expired`, `failed`, `voided`. Returns `400` on unknown value. |
| `date_from` | _(none)_ | Filter to transactions created on or after this date (`YYYY-MM-DD`, inclusive, interpreted as start of day UTC) |
| `date_to` | _(none)_ | Filter to transactions created before or on this date (`YYYY-MM-DD`, inclusive, interpreted as end of day UTC — technically `date_to + 1 day` exclusive) |

Date filters and cursor pagination are compatible — the cursor narrows within whatever date window is set.

Response `200 OK`:
```json
{
  "status": "OK",
  "data": {
    "items": [...],
    "next_cursor": "eyJ0IjoiMjAyNi0wNS0yM...",
    "has_more": true
  }
}
```

When `has_more` is `false`, `next_cursor` is empty and there are no further pages.

Error `400 Bad Request` — invalid status value:
```json
{ "status": "Bad Request", "error": "invalid status: foobar" }
```

### Get payment

```http
GET /api/payments/{id}
X-API-KEY: <key>
X-Tenant-ID: 42
```

Returns `404` if the ID does not exist or belongs to a different tenant.

### Refresh payment

Fetches the latest state from Xendit for a single transaction and updates the DB if status changed. Intended for use when a user clicks a "refresh" button in the UI.

```http
POST /api/payments/{id}/refresh
X-API-KEY: <key>
X-Tenant-ID: 42
```

No request body required.

Response `200 OK` — returns the current transaction (whether or not status changed):
```json
{
  "status": "OK",
  "data": { ...same fields as Get payment... }
}
```

**Short-circuits (returns DB state immediately, no Xendit call) when:**
- Transaction is in a terminal state (`expired`, `failed`, `refunded`, `voided`)
- `provider_invoice_id` starts with `manual-` (no Xendit session to fetch)

**Concurrent update handling:** if a webhook lands and updates the transaction between our DB read and our write, the optimistic lock conflict is caught and the fresh DB state is returned rather than failing.

Note: the refresh endpoint does **not** set `payment_method` because `GET /sessions/{id}` doesn't return channel code. If `payment_method` is needed, wait for the `payment_session.completed` webhook.

### Merchant balance

```http
GET /api/payments/balance
X-API-KEY: <key>
X-Tenant-ID: 42
X-Xendit-Account-ID: xnd_acct_merchant_123
```

Response `200 OK`:
```json
{
  "status": "OK",
  "data": {
    "settled_amount":       9500000,
    "pending_balance":      2000000,
    "pending_platform_fee":   40000,
    "pending_xendit_fee":     15000,
    "pending_vat":             1650,
    "pending_withholding":        0,
    "last_synced_at":      "2026-05-27T18:00:00Z"
  }
}
```

| Field | Source | Description |
|---|---|---|
| `settled_amount` | Xendit `GET /balance` (live) | Funds available for withdrawal in the merchant's sub-account |
| `pending_balance` | `merchant_settlement_snapshots` | SUM of `merchant_amount` for `paid` (not yet settled) transactions |
| `pending_platform_fee` | Snapshot | Platform fee portion of pending transactions |
| `pending_xendit_fee` | Snapshot | Xendit's processing fee on pending transactions (backfilled by nightly sync) |
| `pending_vat` | Snapshot | VAT on the Xendit fee |
| `pending_withholding` | Snapshot | Withholding taxes on pending transactions |
| `last_synced_at` | Snapshot | When the nightly settlement sync last ran; `null` if it has never run |

The `settled_amount` is always fresh (real-time Xendit API call). The pending breakdown is from the last nightly snapshot — it reflects the state as of 1 AM WIB.

### Replay webhook event

```http
POST /api/payments/webhooks/{event_id}/replay
X-API-KEY: <key>
Content-Type: application/json

{ "force": false }
```

Returns `202 Accepted` on success, `409 Conflict` if already processed and `force=false`.

### Xendit webhook callback (public)

```http
POST /webhook/xendit
x-callback-token: <token>
Content-Type: application/json

{
  "event": "payment.capture",
  "created": "2025-06-01T09:55:00Z",
  "data": {
    "payment_id": "py-1402feb0-bb79-47ae-9d1e-e69394d3949c",
    "payment_request_id": "pr-90392f42-d98a-49ef-a7f3-abc123",
    "payment_session_id": "ps-661f87c614802d6c402cd82d",
    "reference_id": "order-ORD-2025-001-attempt-1",
    "status": "SUCCEEDED",
    "channel_code": "QRIS",
    "currency": "IDR",
    "request_amount": 100000
  }
}
```

Always returns `200 OK`. Signature failures are logged but do not return 4xx (to avoid Xendit retry storms on misconfiguration).

---

## 12. Security Design

### Webhook signature

Xendit uses a **static shared token** (not HMAC-SHA). Validation uses `hmac.Equal` for constant-time comparison, which prevents timing side-channel attacks where an attacker could determine valid token prefixes by measuring response times.

Validation is done inside `provider/xendit/signature.go` before any payload is parsed or stored.

### Token misconfiguration

If `XENDIT_WEBHOOK_TOKEN` is empty, signature validation is skipped. This is only safe in local development. In production it must be set.

### Idempotency keys

The `idempotency_key` in `payment_transactions` is a `UNIQUE` index on `(tenant_id, idempotency_key)`. This prevents duplicate charges even if the same request is sent twice.

### Row-level locking

Two `SELECT ... FOR UPDATE` locks are held during webhook processing:

1. On the `payment_webhook_events` row — prevents two workers from processing the same event.
2. On the `payment_transactions` row — prevents concurrent webhooks from applying conflicting state transitions.

Both locks are held within a single `READ COMMITTED` transaction, released on commit.

### Tenant isolation

`GetPayment` verifies `txn.TenantID == requestTenantID` before returning data. Cross-tenant access returns `ErrNotFound` (not 403, to avoid leaking existence).

### Ledger immutability

The `payment_ledger_entries` table has no `UPDATE` or `DELETE` endpoints. The application layer never calls update queries on it. The `reference_id` unique constraint prevents double-insertion.

---

## 13. Observability & Logging

All logs are structured JSON (logrus `JSONFormatter`). Every significant operation emits a log entry with a consistent set of fields.

### Standard fields

| Field               | Where it appears                |
|---------------------|---------------------------------|
| `component`         | Every log entry                 |
| `operation`         | Every log entry                 |
| `request_id`        | HTTP request logs               |
| `tenant_id`         | Every financial operation       |
| `transaction_id`    | Transaction mutations, ledger   |
| `webhook_event_id`  | All webhook processing steps    |
| `provider_event_id` | Webhook ingest + processing     |
| `provider_invoice_id` | Webhook processing, payments  |
| `event_type`        | Webhook processing              |
| `amount`            | Payment creation, ledger writes |
| `currency`          | Payment creation, ledger writes |
| `status`            | Every state transition          |
| `prev_status`       | Every state transition          |
| `duration_ms`       | Webhook processing completion   |
| `attempt`           | Webhook processing attempts     |
| `error`             | All error paths                 |

### Log levels

| Level   | When to expect it |
|---------|-------------------|
| `INFO`  | Every state transition, every ledger write, every webhook processed successfully |
| `WARN`  | Duplicate webhook received, replay triggered, dead-lettered event, signature failure |
| `ERROR` | Processing failed after all retries, DB error, unrecoverable state |

### Tracing a payment end-to-end

To trace a single payment through production logs, filter on `transaction_id`. All logs from create → webhook → ledger → payout share this field.

```bash
# e.g. with Loki / Grafana
{service="connector"} | json | transaction_id="abc-123-..."
```

To trace a webhook: filter on `webhook_event_id`.

To find all failures for a tenant: filter on `tenant_id` + `level=error`.

---

## 14. Database Schema

All tables are in the `public` schema. Run migrations with `go run cmd/migrate/main.go up`.

### payment_transactions

Primary entity. Tracks one payment from creation to settlement.

| Column               | Type             | Notes |
|----------------------|------------------|-------|
| `id`                 | UUID PK          | |
| `tenant_id`          | BIGINT           | Multi-tenant isolation (integer ID from Tokokarya) |
| `order_number`       | TEXT             | Tokokarya order number (string reference) |
| `idempotency_key`    | TEXT             | Unique per tenant — prevents duplicate charges |
| `provider`           | TEXT             | `xendit` (default) |
| `provider_invoice_id`| TEXT nullable    | Xendit `payment_session_id` (ps-xxx) |
| `provider_payment_id`| TEXT nullable    | Xendit `payment_id` (py-xxx); populated on `payment.capture` |
| `checkout_url`       | TEXT nullable    | Xendit `payment_link_url`; returned to the caller on create so the buyer can be redirected |
| `xendit_account_id`  | TEXT nullable    | Merchant's Xendit sub-account ID; sent as `for-user-id` header on API calls and as transfer destination |
| `payment_method`     | TEXT nullable    | `channel_code` from the capture webhook (e.g. `QRIS`, `BCA`); empty until payment is confirmed |
| `payment_channel`    | TEXT nullable    | Same as `payment_method` |
| `amount`             | BIGINT           | Gross amount in smallest currency unit |
| `platform_fee`       | BIGINT           | Always: `merchant_amount + platform_fee = amount` |
| `merchant_amount`    | BIGINT           | |
| `status`             | payment_status   | State machine enum |
| `version`            | INTEGER          | Optimistic lock version; increments on every write |
| `expires_at`         | TIMESTAMPTZ NULL | Session expiry |
| `paid_at`            | TIMESTAMPTZ NULL | Set on `payment.capture` or `payment_session.completed` |
| `settled_at`         | TIMESTAMPTZ NULL | Set by settlement sync job when Xendit confirms `settlement_status = SETTLED` |
| `xendit_fee`         | BIGINT           | Xendit's processing fee; `0` until settlement sync runs |
| `vat`                | BIGINT           | VAT on the Xendit fee; `0` until settlement sync runs |
| `xendit_withholding_tax` | BIGINT       | Xendit's withholding tax; `0` until settlement sync runs |
| `third_party_wht`    | BIGINT           | Third-party withholding tax; `0` until settlement sync runs |
| `estimated_settlement_time` | TIMESTAMPTZ NULL | Xendit's estimated settlement date; backfilled by sync |

### payment_webhook_events

Raw event store. Append-only.

| Column                 | Type                       | Notes |
|------------------------|----------------------------|-------|
| `id`                   | UUID PK                    | |
| `provider_event_id`    | TEXT                       | Unique per provider — the primary dedup key |
| `event_type`           | TEXT                       | `payment.capture`, `payment.failure`, etc. |
| `raw_payload`          | JSONB                      | Unmodified request body — preserved for replay |
| `headers`              | JSONB NULL                 | Request headers for audit |
| `signature_valid`      | BOOLEAN                    | |
| `processing_status`    | webhook_processing_status  | `received → processing → processed / failed / dead_lettered` |
| `processing_attempts`  | INTEGER                    | Incremented on each attempt |
| `transaction_id`       | UUID FK NULL               | Set after successful processing |

### payment_ledger_entries

Immutable double-entry ledger. Never updated or deleted.

| Column          | Type                  | Notes |
|-----------------|-----------------------|-------|
| `tenant_id`     | BIGINT                | |
| `transaction_id`| UUID FK               | |
| `account_type`  | ledger_account_type   | `escrow`, `merchant_payable`, etc. |
| `direction`     | ledger_direction      | `debit` or `credit` |
| `amount`        | BIGINT                | Always positive — direction carries the sign |
| `reference_id`  | TEXT                  | Unique per `(tenant_id, reference_id)` — idempotency key |

### payment_payouts

One row per merchant disbursement. `tenant_id` is `BIGINT`. `for_user_id` stores the merchant's Xendit sub-account ID for use when dispatching the payout (background jobs load the payout by ID and still know which sub-account to target).

### payment_idempotency_keys

Short-lived (24h TTL) request deduplication store for the payment creation API.

### merchant_settlement_snapshots

One row per tenant. Upserted by the nightly settlement sync job. Acts as a read-through cache for the balance endpoint — avoids scanning the full `payment_transactions` table on every request.

| Column               | Type          | Notes |
|----------------------|---------------|-------|
| `id`                 | UUID PK       | |
| `tenant_id`          | BIGINT UNIQUE | One row per tenant |
| `xendit_account_id`  | TEXT          | Merchant's Xendit sub-account ID (used as `for-user-id` header) |
| `pending_balance`    | BIGINT        | SUM of `merchant_amount` for `status = 'paid'` transactions |
| `pending_platform_fee` | BIGINT      | SUM of `platform_fee` for `status = 'paid'` transactions |
| `pending_xendit_fee` | BIGINT        | SUM of `xendit_fee` (backfilled from Xendit `GET /transactions`) |
| `pending_vat`        | BIGINT        | SUM of `vat` |
| `pending_withholding`| BIGINT        | SUM of `xendit_withholding_tax + third_party_wht` |
| `last_synced_at`     | TIMESTAMPTZ NULL | When the sync job last completed for this tenant; `null` means never synced |
| `created_at`         | TIMESTAMPTZ   | |
| `updated_at`         | TIMESTAMPTZ   | |

---

## 15. Adding a New Payment Provider

The `PaymentProvider` interface (`internal/payment/provider/interface.go`) is the only contract you need to implement.

```go
type PaymentProvider interface {
    CreateInvoice(ctx, req CreateInvoiceRequest) (*Invoice, error)           // req.ForUserID sets for-user-id header
    GetInvoice(ctx, invoiceID, forUserID string) (*Invoice, error)
    CancelInvoice(ctx, invoiceID, forUserID string) error
    CreateRefund(ctx, req CreateRefundRequest) (*Refund, error)              // req.ForUserID sets for-user-id header
    ValidateWebhookSignature(ctx, payload, headers) error
    ParseWebhookEvent(ctx, payload) (*WebhookEvent, error)
    CreatePayout(ctx, req CreatePayoutRequest) (*Payout, error)              // req.ForUserID sets for-user-id header
    Transfer(ctx, req TransferRequest) (*TransferResponse, error)            // routes funds to sub-account
    ProviderName() string
}
```

All request structs that make outbound Xendit API calls carry a `ForUserID string` field. When non-empty, the `for-user-id: <value>` HTTP header is added, scoping the operation to that sub-account. `Transfer` is the only method that intentionally omits this header (it is a platform-level operation).

Steps:
1. Create `internal/payment/provider/midtrans/` and implement the interface.
2. Map your provider's event types to the canonical scheme used by the processor: `payment.capture` (success), `payment.failure` (failed/expired), `payment.authorization` (authorised pending capture). Inside `ParseWebhookEvent`, set `FailureCode` to `"PAYMENT_REQUEST_EXPIRED"` to trigger the `expired` transition rather than `failed`.
3. In `http/main.go`, construct your provider and pass it to `RegisterWebhookHandlerV2`.
4. Add a new webhook endpoint (e.g. `/webhook/midtrans`) if the provider uses a different callback URL.

The rest of the system — webhook processor, ledger service, replay — requires no changes.

---

## 16. Configuration

Environment variables (set in `.env` or shell):

| Variable               | Required | Default                    | Description |
|------------------------|----------|----------------------------|-------------|
| `DATABASE_DSN`         | Yes*     | —                          | PostgreSQL connection string. If unset, the payment module is disabled and the legacy webhook handler is used. |
| `XENDIT_API_KEY`       | Yes      | —                          | Xendit secret API key |
| `XENDIT_BASE_URL`      | No       | `https://api.xendit.co`    | Override for testing |
| `XENDIT_WEBHOOK_TOKEN` | Yes**    | —                          | Webhook callback token from Xendit dashboard |
| `REDIS_URL`            | No       | `redis://localhost:6379`   | Redis for asynq task queue |
| `ALLOWED_API_KEYS`     | Yes      | —                          | Comma-separated API keys for authenticated routes |
| `PORT`                 | No       | `8000`                     | HTTP server port |

`*` Without `DATABASE_DSN`, create-payment and webhook processing are unavailable. The XenPlatform account API still works.

`**` Without `XENDIT_WEBHOOK_TOKEN`, webhook signature validation is skipped. Never leave this unset in production.

---

## 17. Running & Migrations

### Apply migrations

```bash
# First time setup
go run cmd/migrate/main.go up

# Check current version
go run cmd/migrate/main.go version

# Roll back one migration
go run cmd/migrate/main.go down

# Roll back everything
go run cmd/migrate/main.go down 0
```

Migration files live in `db/migrations/` and follow the `golang-migrate` naming convention: `000001_name.up.sql` / `000001_name.down.sql`.

### Start the service

```bash
# Development
go run http/*.go

# Docker
docker build -t connector .
docker run -p 8000:8000 --env-file .env connector
```

The payment module activates automatically when `DATABASE_DSN` is set.

### Test webhook locally

Use [ngrok](https://ngrok.com) or any HTTP tunnel to expose your local server, then point Xendit's callback URL to `/webhook/xendit`.

```bash
ngrok http 8000
# Set callback URL in Xendit dashboard: https://<ngrok-id>.ngrok.io/webhook/xendit
```

---

## 18. Failure Scenarios

| Scenario | What happens | Recovery |
|---|---|---|
| Worker crashes mid-transaction | DB transaction rolls back automatically. asynq retries the task. Webhook event status resets to `received` on replay. | Automatic. |
| Two workers pick up the same task | First worker acquires the `FOR UPDATE` lock. Second worker blocks until first commits, then sees `processing_status = processed` and returns nil. | No action needed. |
| Xendit sends duplicate webhook | `ON CONFLICT DO NOTHING` on `provider_event_id` drops the duplicate at insert time. Returns 200 without enqueuing. | Automatic. |
| DB down during webhook receive | Event not stored. Xendit will retry the callback (Xendit retries for up to 24h). When DB recovers, the next retry is processed normally. | Wait for DB recovery. |
| DB down during webhook processing | Transaction rolled back. asynq retries with backoff. | Automatic after DB recovery. |
| Ledger imbalance detected | `LedgerJournal.Validate()` returns an error before any DB write. Webhook processing fails and is retried — but will keep failing until the bug is fixed. | Fix the bug, then force-replay the event. |
| Provider invoice paid but never ingested (webhook lost) | Reconciliation: periodically compare Xendit invoice list against local transactions. Flag mismatches. | Manual correction or reconciliation job (not yet built). |
| Payment expires locally but Xendit still accepts payment | Webhook arrives for an expired transaction. `awaiting_payment → paid` is a valid transition from `expired`? No — `expired` is terminal. The webhook is marked `failed` with an invalid-transition error. | Investigate in Xendit dashboard. May need a manual override if payment was genuinely received. |
| Auto-transfer to merchant sub-account fails | Transfer failure is logged at `ERROR` level but does NOT fail the webhook processing — the payment DB record is already correct. The `merchant_amount` remains in `merchant_payable` in the ledger. | Investigate the error log (filter on `component=webhook_processor` + `transaction_id`). Retry via the `POST /transfers` Xendit API directly, or trigger a payout via `PayoutService.DispatchPayout` which also routes funds. |
