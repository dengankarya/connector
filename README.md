# Connector

A Go microservice that bridges [Tokokarya](https://tokokarya.com) (Indonesian ecommerce platform) with external providers for payments, shipping, and regional data.

## Purpose

Connector centralizes integrations with third-party APIs behind a single internal interface. Instead of Tokokarya's backend talking directly to multiple providers (payment gateways, logistics networks, region databases), all protocol translation, validation, and error handling is owned by this service.

## Architecture

```
Tokokarya Backend
       │
       ▼
  Connector (this service)
       │
       ├─── Payment Gateway (DOKU)
       ├─── Shipping Provider (Biteship)
       └─── Region Data (Wilayah.id)
```

### Module Organization

```
internal/
  payment/          Payment processing (DOKU gateway integration)
    domain/         Domain entities and state machine
    repository/     Database access layer
    service/        Business logic
    webhook/        Async webhook processing pipeline
    jobs/           Background jobs (expiry, retry)
    
  shipping/         Biteship shipping integration
    domain/         Shipment entities and statuses
    repository/     Database access
    service/        Logistics service facade
    webhook/        Webhook receiver and processor
    
  account/          Merchant accounts and DOKU sub-account management
  region/           Indonesian region/administrative hierarchy lookups
  geocoding/        Geoapify geocoding service
  
  worker/           Asynq task queue worker setup

pkg/
  postgres/         Shared PostgreSQL infrastructure
  biteship/         Biteship HTTP client
  doku/             DOKU payment gateway client
  tokokarya/        Tokokarya webhook forwarder
  wilayah/          Wilayah.id region client
  geoapify/         Geoapify geocoding client
  dbconn/           Database connection helpers
```

## Authentication

All API endpoints (except public webhooks) require an `X-API-KEY` header. Keys are configured via comma-separated `ALLOWED_API_KEYS` env var. Admin-only endpoints require keys from `ADMIN_API_KEYS`.

Multi-tenant routes also require `X-Tenant-ID` (int64 header).

## Running

```bash
# Start the HTTP server + embedded asynq worker
make run
# or
go run http/*.go

# Run database migrations
go run cmd/migrate/main.go up
go run cmd/migrate/main.go down
go run cmd/migrate/main.go version

# Build Docker image
make docker-build
```

## Database

- **Driver:** PostgreSQL via `jackc/pgx/v5`
- **Migrations:** `golang-migrate` format, stored in `db/migrations/`
- **Transaction pattern:** Context-based propagation via `pkg/postgres`; repositories read `pgx.Tx` from context

## Background Jobs

Work is queued to Redis via asynq:
- `webhooks` queue (weight 6) — payment webhook processing, higher priority
- `default` queue (weight 4) — other background tasks

Periodic jobs (every 5–10 min) and one-shot tasks (scheduled cancellation) are registered via `asynq.Scheduler`.

## Configuration

Environment variables (see `config/config.go`):

| Var | Purpose |
|---|---|
| `ENV` | Runtime environment (`development` / `production`) |
| `PORT` | HTTP listen port |
| `DATABASE_DSN` | PostgreSQL connection string (optional; payment module disabled if absent) |
| `REDIS_URL` | Redis connection for asynq task queue |
| `ALLOWED_API_KEYS` | Comma-separated regular API keys |
| `ADMIN_API_KEYS` | Comma-separated admin API keys |
| `DOKU_CLIENT_ID`, `DOKU_SECRET_KEY` | DOKU payment gateway credentials |
| `BITESHIP_API_KEY`, `BITESHIP_BASE_URL` | Biteship shipping provider |
| `BITESHIP_WEBHOOK_SIGNATURE_KEY`, `BITESHIP_WEBHOOK_SIGNATURE_VALUE` | Biteship webhook auth |
| `WILAYAH_BASE_URL` | Indonesian region data API (default: `https://wilayah.id`) |
| `GEOAPIFY_API_KEY` | Geocoding service |
| `TOKOKARYA_URL`, `TOKOKARYA_API_KEY` | Downstream webhook forwarding target |

## Tech Stack

- **Language:** Go 1.26+
- **Web Framework:** Fiber v3
- **Database:** PostgreSQL 14+ with pgx v5
- **Task Queue:** Asynq (Redis-backed)
- **Logging:** Logrus (structured JSON)
- **API Docs:** Swagger / OpenAPI (via swaggo)

## Key Principles

- **Domain-Driven Design:** Domain entities live in `domain/` packages; providers abstract external APIs
- **Layered Architecture:** Controllers → Services → Repositories → Database
- **Context-Based Transactions:** Repos detect transaction from context; no explicit connection passing
- **Idempotency:** Critical paths (webhooks, payments) use reference IDs to prevent duplicate processing
- **Async-First Webhooks:** Inbound webhooks are validated, stored, then processed asynchronously
- **Tenant Isolation:** All multi-tenant data is scoped by `tenant_id`
