# feat: confirm shipment, domain errors, webhook hold sync, unified balance

## Overview

This PR covers four related areas: confirming a Biteship draft order through the API, a structured domain error system, automatic shipping hold management via webhooks, and a unified merchant balance endpoint.

---

## 1. Confirm Draft Shipment — `POST /shipments/:id/confirm`

### What changed
- **`pkg/biteship/client.go`** — Added `ConfirmShipment(ctx, providerDraftOrderID)` which calls Biteship's `POST /v1/draft_orders/:id/confirm`.
- **`pkg/biteship/mapper.go`** — Added `mapConfirmOrderResponse` to handle the confirm response shape where `id` = live order ID and `draft_order_id` = original draft ID (different from create response where `id` = draft ID).
- **`pkg/biteship/dto.go`** — Added `DraftOrderID *string` to `CreateOrderResponse` (used by both create and confirm responses).
- **`internal/shipping/provider/interface.go`** — Added `ConfirmShipment(ctx, providerDraftOrderID string)` to the `ShippingProvider` interface.
- **`internal/shipping/service.go`** — Added `ConfirmShipment(ctx, tenantID, id)` which: fetches the shipment, enforces tenant ownership, validates balance (see §3), calls the provider, and persists the updated record (live order ID, status, tracking info).
- **`internal/shipping/controller.go`** — Added `handleConfirmShipment` with Swagger annotations; registered as `POST /shipments/:id/confirm`.

### Response shape
On success returns the full shipment object with `provider_order_id` populated (was null when draft).

### Error responses
| Status | Code | Reason |
|--------|------|--------|
| 400 | `BR_MISSING_TENANT_ID` | `X-Tenant-ID` header missing |
| 400 | `BR_INVALID_SHIPMENT_ID` | UUID parse failure |
| 402 | `PR_INSUFFICIENT_BALANCE` | Merchant shipping balance too low |
| 404 | `NF_SHIPMENT_NOT_FOUND` | Shipment not found or belongs to another tenant |
| 500 | `IN_INTERNAL_ERROR` | Provider or DB error |

---

## 2. Structured Domain Error System

### Problem
Errors were scattered `errors.New` sentinels across packages. Controllers needed a growing chain of `errors.Is` checks per handler, and HTTP status mapping was manual.

### Solution — `common.DomainError`

```go
// Code prefix determines HTTP status automatically:
// BR_ → 400  AU_ → 401  PR_ → 402  FB_ → 403
// NF_ → 404  CF_ → 409  IN_ → 500

var ErrInsufficientBalance = common.NewDomainError("PR_INSUFFICIENT_BALANCE", "insufficient balance")
```

**`common/errors.go`** — new file:
- `DomainError` struct with `Code` + `Message`
- `HTTPStatus()` — derives HTTP status from code prefix
- `Is()` — enables `errors.Is` comparison by code even through `fmt.Errorf("%w", ...)` wrapping
- Pre-defined shared errors: `ErrNotFound`, `ErrInsufficientBalance`, `ErrBadRequest`, `ErrForbidden`, `ErrConflict`, `ErrUnauthorized`, `ErrInternal`

**`common/response.go`** — added:
- `ErrorDetail{Code, Message}` — structured error body
- `common.Err(statusText, code, message)` — convenience constructor

**Domain sentinels updated to `*DomainError`:**
- `internal/account/domain.go` — all 6 sentinels
- `internal/shipping/domain/shipment.go` — `ErrNotFound`
- `internal/shipping/service.go` — `ErrInvalidCursor`

### Controller pattern (confirm endpoint as the pilot)
```go
// Before — growing chain
if errors.Is(err, domain.ErrNotFound) { ... }
if errors.Is(err, account.ErrInsufficientBalance) { ... }

// After — one block handles all domain errors
var de *common.DomainError
if errors.As(err, &de) {
    status := de.HTTPStatus()
    return c.Status(status).JSON(common.Err(http.StatusText(status), de.Code, de.Message))
}
```

> Other endpoints will adopt this pattern incrementally.

---

## 3. Balance Validation Before Confirm

### What changed
- **`internal/account/service.go`** — Added `ValidateShippingConfirm(ctx, tenantID, orderNumber, requiredAmount)`:
  - If an active hold exists for the order → pass (funds already reserved via the hold flow)
  - If no hold → check `available_balance >= shipping_cost`; return `ErrInsufficientBalance` otherwise
- **`internal/shipping/service.go`** — Added `BalanceValidator` interface; wired into `ShippingService`; called in `ConfirmShipment` before hitting the Biteship API.
- **`http/main.go`** — Passes `balanceSvc` as the `BalanceValidator` when constructing `ShippingService`.

The validator is `nil`-safe: when `DATABASE_DSN` is not set the check is skipped.

---

## 4. Automatic Shipping Hold Management via Webhook

### Problem
The Biteship `order.status` webhook was already updating the `shipments` table synchronously, but the `shipping_holds` table was never touched — hold confirmation/release was fully manual.

### What changed
- **`internal/account/repository.go`** — Added `GetHoldByOrderNumber(ctx, tenantID, orderNumber)` — queries for the active (`status = 'holding'`) hold for a given order.
- **`internal/account/service.go`** — Added:
  - `ConfirmHoldForOrder(ctx, tenantID, orderNumber)` — looks up hold by order number then confirms; no-op if no active hold or already actioned (idempotent).
  - `ReleaseHoldForOrder(ctx, tenantID, orderNumber)` — same pattern, releases hold and returns funds to available.
- **`internal/shipping/webhook.go`** — Added `HoldManager` interface; added `holds HoldManager` field to `webhookController`; after updating shipment status in `handleOrderStatus`, automatically:
  - `confirmed` / `scheduled` → `ConfirmHoldForOrder` (funds disbursed)
  - `cancelled` → `ReleaseHoldForOrder` (funds returned to available)
  - Hold errors are logged but do **not** fail the webhook response to Biteship.
- **`http/main.go`** — Passes `balanceSvc` as `HoldManager` to `RegisterWebhookHandler`.

### Idempotency
Both `ConfirmHoldForOrder` and `ReleaseHoldForOrder` are idempotent:
- If no active hold exists for the order number → no-op (merchant may not have used the hold flow)
- If hold is already confirmed/released → no-op (duplicate webhook delivery)

---

## 5. Unified Balance Endpoint

- **`GET /accounts/balance`** now returns both the shipping wallet **and** the payment settlement balance in one response:

```json
{
  "status": "OK",
  "data": {
    "shipping": {
      "available": 500000,
      "on_hold": 35000,
      "currency": "IDR"
    },
    "payment": {
      "settled": 1200000,
      "pending_settlement": 300000,
      "paid_out": 900000,
      "available_to_payout": 300000,
      "currency": "IDR"
    }
  }
}
```

- **`GET /accounts/transactions/:id`** now returns `metadata` for payment-type items (populated from `payment_transactions.metadata`).

---

## Checklist

- [x] Build passes (`go build ./...`)
- [x] Swagger annotations updated for new/changed endpoints
- [x] Domain errors carry HTTP status in code prefix — no manual status mapping needed
- [x] Webhook hold management is idempotent — safe for duplicate delivery
- [x] Balance validator is nil-safe — works without `DATABASE_DSN`
- [x] Confirm response correctly maps `id` (live order) vs `draft_order_id` (original draft)
