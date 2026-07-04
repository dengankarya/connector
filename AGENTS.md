# AGENTS.md

Module: `github.com/dengankarya/connector` (Go 1.26, Fiber v3, pgx/v5, asynq).

## Commands

```bash
make run                  # http/main.go (+ embedded asynq worker)
go run cmd/migrate/main.go up  # standalone migration runner
make docs                 # swag fmt + swag init (output: docs/)
make migrate-new name=x   # creates db/migrations/NNNNNN_x.{up,down}.sql
make generate-client      # OpenAPI → TypeScript fetch client
make docker-build         # docker build -t connector .
make cf-tunnel            # cloudflared tunnel (localhost:8002 → *.dengankarya.com)
```

`.env` is auto-loaded via `godotenv/autoload` in `config/config.go`.

## Architecture

**Two entrypoints:**
- `http/main.go` — HTTP server, embedded asynq worker, asynq scheduler (cron jobs)
- `cmd/migrate/main.go` — `golang-migrate` CLI for manual migration control

**Webhook pipeline (critical path):**
1. `POST /webhook/{doku,xendit}` — public routes (no auth), registered on `apiRootGroup` BEFORE the auth middleware group
2. Ingest handler stores raw event → enqueues asynq task → returns HTTP 200
3. Asynq worker calls `Processor.Process` which holds `SELECT FOR UPDATE` on both the webhook event row and the payment transaction row
4. All mutations (state transition + ledger entries + mark processed) in one DB transaction via `TxRunner.RunInTx`
5. Forward to Tokokarya is fire-and-forget (goroutine) post-commit

**Payment status state machine** (`internal/payment/domain/transaction.go`):
```
pending → awaiting_payment → paid → settled → refunding → refunded
                           ↘ expired / failed / voided (terminals)
```
Enforced by `TransitionTo()` — never mutate `txn.Status` directly.

## Important conventions

- **All financial mutations** go through `TxRunner.RunInTx`
- **Ledger entries are append-only**; `reference_id` is the idempotency key
- **`merchant_amount + platform_fee + shipping_fee = amount`** — enforced by DB CHECK constraint
- **asynq task type strings** (`internal/worker/tasks.go`) must match between enqueuer and handler exactly
- Queue weights: `webhooks=6`, `default=4`
- Periodic jobs via `asynq.Scheduler`: expire invoices every 5min, retry webhooks every 10min, sync Xendit settlements every 6h

## Payment module is optional

If `DATABASE_DSN` is unset, payment/account/shipping repositories are nil. Module constructors return partial structs; fields like `PaymentService`, `WebhookHandler`, `EventRepo` will be nil. Always guard with `if mod.Field != nil` before using.

## Auth

- `X-API-KEY` header validated against comma-separated `ALLOWED_API_KEYS` (+ `ADMIN_API_KEYS` implicitly for regular auth)
- `adminRequest` middleware must be used AFTER `authenticatedRequest`
- Webhook routes (`/webhook/*`) are public — registered on `apiRootGroup` before the auth middleware
- Multi-tenant endpoints also require `X-Tenant-ID` (int64 header)
- `/monitor` (asynqmon dashboard) has no API key — protected at infra level via Cloudflare Access

## Module structure

Each domain module has a `module.go` with a `NewModule()` constructor that handles dependency injection:

```
internal/payment/   → NewModule(pool, enqueuer, providers, accountFinder, forwarder, logger)
internal/account/   → NewModule(pool, txRunner, dokuClient, xenditClient, logger)
internal/shipping/  → NewModule(pool, logisticsClient, shippingProvider, accountManager, logger)
```

Region and geocoding bypass the module pattern — they're wired directly in `http/main.go` using a cache decorator pattern (`NewCachedClient`, `NewCachedGeocoder`).

## Payment providers

Two active providers: **DOKU** and **Xendit** (both implement `provider.PaymentProvider`). Provider map is constructed in `http/main.go` and passed to `payment.NewModule`. Add a new gateway by implementing the interface in `internal/payment/provider/interface.go`.

Manual payments: `provider_invoice_id = "manual-<uuid>"`, immediately transition `paid → settled` in one tx.

## DB conventions

- pgx/v5 pool — never `database/sql`
- Context-based tx propagation: `postgres.WithTx(ctx, tx)`, repos call `postgres.DBFromContext(ctx, pool)`
- Migrations auto-run at `http/main.go` startup AND available standalone via `cmd/migrate/main.go`
- UUIDs via `gen_random_uuid()` (pgcrypto, migration 000001)

## REST conventions

- OpenAPI at `/swagger/` (swaggo), JSON spec at `/swagger/doc.json`
- Base path: `/api/v1`
- Pagination: cursor-based, opaque base64 token
- Tenant isolation: scoped by `tenant_id` in queries
