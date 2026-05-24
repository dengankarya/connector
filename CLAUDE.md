# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
# Run the HTTP server
make run
# or: go run http/*.go

# Run database migrations
go run cmd/migrate/main.go up
go run cmd/migrate/main.go down
go run cmd/migrate/main.go version

# Build Docker image
make docker-build
```

There are no tests in this codebase yet.

## Architecture

This is a Go microservice (module: `github.com/dengankarya/overwatch`) that sits between **Tokokarya** (ecommerce platform) and external services (Xendit, Biteship, Wilayah.id). It handles payment processing, shipping aggregation, and Indonesian region lookups.

**Two entry points:**
- `http/main.go` — HTTP server + embedded asynq worker
- `cmd/migrate/main.go` — golang-migrate CLI

### Payment Module (`internal/payment/`)

The most complex part. Layered: `controller → service → provider/repository`.

**Webhook pipeline** (the critical path):
1. `POST /webhook/xendit` receives the callback and immediately stores the raw event (`Processor.Ingest`)
2. Returns HTTP 200 — processing is async
3. An asynq worker picks it up (`webhook/handler.go`) and calls `Processor.Process`
4. `Process` holds two `SELECT FOR UPDATE` locks — one on the webhook event row (prevents duplicate workers), one on the payment transaction row (prevents concurrent webhooks racing on the same invoice)
5. State transition + ledger entries + event marked processed all commit in one DB transaction
6. Raw payload is forwarded to Tokokarya post-commit in a fire-and-forget goroutine

**Invariants that must never be broken:**
- All financial mutations go through `TxRunner.RunInTx`
- Ledger entries are append-only; `reference_id` is the idempotency key per entry
- Webhook processing always `SELECT FOR UPDATE` both the event row and the transaction row before any mutation
- `payment_transactions.merchant_amount + platform_fee = amount` enforced by DB check constraint

**Provider abstraction** (`internal/payment/provider/interface.go`): `PaymentProvider` interface decouples the payment logic from Xendit. Adding a new gateway = implementing this one interface.

**Payment modes:**
- Xendit: `POST /api/payments/` → connector calls Xendit Sessions API → returns `checkout_url`
- Manual: `POST /api/payments/manual` → no external call → returns `provider_invoice_id = "manual-<uuid>"` which Tokokarya uses as `payment_session_id` when sending the confirmation to `POST /webhook/xendit`

**Payment module is optional** — if `DATABASE_DSN` is not set, the payment module is disabled and the legacy XenPlatform account webhook handler is used instead.

### Asynq Worker

Runs inside the same process as the HTTP server. Two queues:
- `webhooks` (weight 6) — payment webhook processing
- `default` (weight 4) — XenPlatform account updates

Task type constants live in `internal/worker/tasks.go` — the enqueuer and handler must use the same string.

Periodic jobs registered via `asynq.Scheduler`: expire stale invoices every 5 min, retry failed webhooks every 10 min.

### Other Modules

`internal/shipping/` and `internal/region/` use a **decorator cache pattern**: `NewCachedAggregator(biteshipClient)` wraps the HTTP client with an in-memory cache. The cache layer is transparent to the service.

### Auth

`X-API-KEY` header validated against comma-separated `ALLOWED_API_KEYS` env var. Applied to all routes except `POST /webhook/xendit`. Multi-tenant endpoints also require `X-Tenant-ID` (int64 header).

### DB conventions

- PostgreSQL via `pgx/v5` pool — never `database/sql`
- Context-based transaction propagation: `repository.WithTx(ctx, tx)` injects a `pgx.Tx`; `dbFromContext(ctx, pool)` returns it or falls back to the pool
- Migrations: `golang-migrate` format, files in `db/migrations/000NNN_name.{up,down}.sql`
- UUIDs: `gen_random_uuid()` via `pgcrypto` extension (enabled in migration 000001)
