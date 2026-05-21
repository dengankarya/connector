# Connector

A wrapper service for [DenganKarya](https://dengankarya.com) that abstracts communication with third-party APIs behind a single internal HTTP interface.

## Overview

Connector sits between DenganKarya's backend and external logistics/shipping providers. Instead of each service talking directly to third-party APIs, all that complexity is centralised here — auth, response normalisation, and caching.

**Current integrations**

| Provider | Purpose |
|---|---|
| Biteship | Courier list |
| Wilayah.id | Indonesian administrative regions |
| Xendit (XenPlatform) | Sub-account management |

## Architecture

```
DenganKarya backend
       │
       ▼
  Connector (this service)
       │
       ├── in-memory cache (24h TTL)
       │
       ▼
  Third-party APIs (Biteship, Wilayah.id, Xendit, ...)
```

## Authentication

All endpoints except the Xendit webhook require an `X-API-KEY` header.

```
X-API-KEY: <your-key>
```

Keys are configured via the `ALLOWED_API_KEYS` env var (comma-separated).

---

## API Reference

All responses share a common envelope:

```json
{
  "status": "OK",
  "data": { ... },
  "error": null
}
```

---

### Shipping

#### List couriers

```
GET /api/shippings/couriers
```

**Response** `200 OK`

```json
{
  "status": "OK",
  "data": [
    {
      "courier_name": "JNE",
      "courier_code": "jne",
      "courier_service_name": "Reguler",
      "courier_service_code": "REG",
      "tier": "economy",
      "description": "...",
      "service_type": "parcel",
      "shipping_type": "now",
      "shipment_duration_range": "1-3",
      "shipment_duration_unit": "days",
      "available_collection_method": ["pickup", "dropoff"],
      "available_for_cash_on_delivery": true,
      "available_for_proof_of_delivery": false,
      "available_for_instant_waybill_id": false
    }
  ]
}
```

---

### Regions

#### List provinces

```
GET /api/regions/provinces
```

**Response** `200 OK`

```json
{
  "status": "OK",
  "data": [
    { "code": "11", "name": "Aceh" }
  ]
}
```

#### List regencies

```
GET /api/regions/regencies/:province_code
```

| Param | Description |
|---|---|
| `province_code` | Province code from `/provinces` |

**Response** `200 OK`

```json
{
  "status": "OK",
  "data": [
    { "code": "1101", "name": "Kabupaten Simeulue" }
  ]
}
```

#### List districts

```
GET /api/regions/districts/:regency_code
```

| Param | Description |
|---|---|
| `regency_code` | Regency code from `/regencies/:province_code` |

**Response** `200 OK`

```json
{
  "status": "OK",
  "data": [
    { "code": "1101010", "name": "Teupah Selatan" }
  ]
}
```

#### List villages

```
GET /api/regions/villages/:district_code
```

| Param | Description |
|---|---|
| `district_code` | District code from `/districts/:regency_code` |

**Response** `200 OK`

```json
{
  "status": "OK",
  "data": [
    { "code": "1101010001", "name": "Latiung" }
  ]
}
```
k
---

### Payments (XenPlatform)

#### Create account

```
POST /api/payments/accounts
```

**Request body**

```json
{
  "email": "angie@pinkpanther.com",
  "type": "OWNED",
  "public_profile": {
    "business_name": "Owned Business Account"
  }
}
```

| Field | Type | Required | Description |
|---|---|---|---|
| `email` | string | yes | Account email address |
| `type` | string | yes | `OWNED` or `MANAGED` |
| `public_profile.business_name` | string | no | Display name for the account |

**Response** `201 Created`

```json
{
  "status": "Created",
  "data": {
    "id": "6xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
    "email": "angie@pinkpanther.com",
    "type": "OWNED",
    "public_profile": {
      "business_name": "Owned Business Account"
    },
    "status": "REGISTERED",
    "country": "ID",
    "created": "2024-01-01T00:00:00.000Z",
    "updated": "2024-01-01T00:00:00.000Z"
  }
}
```

#### Get account

```
GET /api/payments/accounts/:id
```

| Param | Description |
|---|---|
| `id` | Account ID |

**Response** `200 OK`

```json
{
  "status": "OK",
  "data": {
    "id": "6xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
    "email": "angie@pinkpanther.com",
    "type": "OWNED",
    "public_profile": {
      "business_name": "Owned Business Account"
    },
    "status": "REGISTERED",
    "country": "ID",
    "created": "2024-01-01T00:00:00.000Z",
    "updated": "2024-01-01T00:00:00.000Z"
  }
}
```

---

### Webhooks

#### Xendit webhook receiver

```
POST /webhook/xendit
```

This endpoint is **public** (no `X-API-KEY` required). Requests are validated using the `x-callback-token` header against the value configured in `XENDIT_WEBHOOK_TOKEN`.

**Headers**

| Header | Description |
|---|---|
| `x-callback-token` | Token from Xendit dashboard → Settings → Webhooks |

**Request body** — sent by Xendit, varies by event type:

```json
{
  "event": "account.created",
  "business_id": "...",
  "created": "2024-01-01T00:00:00.000Z",
  "data": { ... }
}
```

**Response** `200 OK`

```json
{
  "status": "OK",
  "data": { ... }
}
```

---

## Configuration

| Env var | Required | Default | Description |
|---|---|---|---|
| `ENV` | no | — | `development` or `production` |
| `PORT` | no | — | HTTP listen port |
| `ALLOWED_API_KEYS` | yes | — | Comma-separated valid API keys |
| `BITESHIP_API_KEY` | yes | — | Biteship API key |
| `BITESHIP_BASE_URL` | yes | — | e.g. `https://api.biteship.com` |
| `WILAYAH_BASE_URL` | no | `https://wilayah.id` | Wilayah.id base URL |
| `XENDIT_API_KEY` | yes | — | Xendit secret key |
| `XENDIT_BASE_URL` | no | `https://api.xendit.co` | Xendit base URL |
| `XENDIT_WEBHOOK_TOKEN` | no | — | Webhook callback verification token |

## Running

```bash
go run ./http
```
