# syntax=docker/dockerfile:1

# ─── Stage 1: build ───────────────────────────────────────────────────────────
FROM golang:1.26-alpine AS builder

WORKDIR /app

# Resolve dependencies first so this layer is cached between source changes.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build \
      -o /app/service \
      ./http

# ─── Stage 2: runtime ─────────────────────────────────────────────────────────
# alpine gives us sh + wget (needed by Coolify's built-in healthcheck) at ~5 MB.
FROM alpine:3.21

RUN apk add --no-cache curl

WORKDIR /app

COPY --from=builder /app/service /app/service
COPY --from=builder /app/db/migrations /app/db/migrations

EXPOSE 8000

HEALTHCHECK --interval=60s --timeout=5s --start-period=10s --retries=3 \
  CMD curl -sf http://localhost:8000/health || exit 1

ENTRYPOINT ["/app/service"]
