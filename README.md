# Connector

A wrapper service for [DenganKarya](https://dengankarya.com) that abstracts communication with third-party APIs behind a single internal HTTP interface.

## Overview

Connector sits between DenganKarya's backend and external logistics/shipping providers. Instead of each service talking directly to third-party APIs, all that complexity is centralised here — auth, response normalisation, and caching.

**Current integrations**

| Provider | API |
|---|---|
| Biteship | Courier list |

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
  Third-party APIs (Biteship, ...)
```

## Endpoints

All endpoints require an `X-API-KEY` header.

## Configuration

| Env var | Description |
|---|---|
| `ENV` | Environment (`development`, `production`) |
| `PORT` | HTTP port (default `9000`) |
| `ALLOWED_API_KEYS` | Comma-separated list of valid API keys |
| `BITESHIP_API_KEY` | Biteship API key |
| `BITESHIP_BASE_URL` | Biteship base URL (e.g. `https://api.biteship.com`) |

## Running

```bash
go run ./http
```
