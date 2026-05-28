# Graph Report - .  (2026-05-27)

## Corpus Check
- Corpus is ~40,520 words - fits in a single context window. You may not need a graph.

## Summary
- 766 nodes · 959 edges · 69 communities (44 shown, 25 thin omitted)
- Extraction: 92% EXTRACTED · 8% INFERRED · 0% AMBIGUOUS · INFERRED: 78 edges (avg confidence: 0.81)
- Token cost: 9,800 input · 3,100 output

## Community Hubs (Navigation)
- [[_COMMUNITY_Swagger Schema Properties|Swagger Schema Properties]]
- [[_COMMUNITY_Swagger API Responses|Swagger API Responses]]
- [[_COMMUNITY_Swagger Shipment & Courier Fields|Swagger Shipment & Courier Fields]]
- [[_COMMUNITY_Payment Domain & State Machine|Payment Domain & State Machine]]
- [[_COMMUNITY_Swagger Payment Fields|Swagger Payment Fields]]
- [[_COMMUNITY_Double-Entry Ledger Domain|Double-Entry Ledger Domain]]
- [[_COMMUNITY_Xendit Provider Integration|Xendit Provider Integration]]
- [[_COMMUNITY_Swagger Shipping Address Fields|Swagger Shipping Address Fields]]
- [[_COMMUNITY_Swagger Shipping Entities|Swagger Shipping Entities]]
- [[_COMMUNITY_Biteship Shipping Client|Biteship Shipping Client]]
- [[_COMMUNITY_Swagger Region Endpoints|Swagger Region Endpoints]]
- [[_COMMUNITY_Biteship Data Transfer Objects|Biteship Data Transfer Objects]]
- [[_COMMUNITY_Shipment Domain & Repository|Shipment Domain & Repository]]
- [[_COMMUNITY_Webhook Event Domain|Webhook Event Domain]]
- [[_COMMUNITY_Region Cache & Wilayah Client|Region Cache & Wilayah Client]]
- [[_COMMUNITY_Webhook Processing Engine|Webhook Processing Engine]]
- [[_COMMUNITY_Payment Service Layer|Payment Service Layer]]
- [[_COMMUNITY_Swagger API Metadata|Swagger API Metadata]]
- [[_COMMUNITY_Payout Domain & Repository|Payout Domain & Repository]]
- [[_COMMUNITY_Payment Provider Interface|Payment Provider Interface]]
- [[_COMMUNITY_HTTP Server & Infra Bootstrap|HTTP Server & Infra Bootstrap]]
- [[_COMMUNITY_Payment Domain Errors|Payment Domain Errors]]
- [[_COMMUNITY_Architecture Documentation|Architecture Documentation]]
- [[_COMMUNITY_Payment Module Docs|Payment Module Docs]]
- [[_COMMUNITY_Payment DB Repository|Payment DB Repository]]
- [[_COMMUNITY_Shipping Provider Interface|Shipping Provider Interface]]
- [[_COMMUNITY_Ledger Service Implementation|Ledger Service Implementation]]
- [[_COMMUNITY_Idempotency Repository|Idempotency Repository]]
- [[_COMMUNITY_Cache Layer Architecture|Cache Layer Architecture]]
- [[_COMMUNITY_Region Service|Region Service]]
- [[_COMMUNITY_Payment HTTP Models|Payment HTTP Models]]
- [[_COMMUNITY_Tokokarya Platform Client|Tokokarya Platform Client]]
- [[_COMMUNITY_Service Overview|Service Overview]]
- [[_COMMUNITY_Auth & Manual Payment|Auth & Manual Payment]]
- [[_COMMUNITY_Region API Controller|Region API Controller]]
- [[_COMMUNITY_Asynq Worker Server|Asynq Worker Server]]
- [[_COMMUNITY_Shipping Service|Shipping Service]]
- [[_COMMUNITY_Account Management Service|Account Management Service]]
- [[_COMMUNITY_Payout Service|Payout Service]]
- [[_COMMUNITY_XenPlatform Account Client|XenPlatform Account Client]]
- [[_COMMUNITY_Transaction Safety Patterns|Transaction Safety Patterns]]
- [[_COMMUNITY_Webhook Retry & Replay|Webhook Retry & Replay]]
- [[_COMMUNITY_Shipping Cache Aggregator|Shipping Cache Aggregator]]
- [[_COMMUNITY_Shipping API Controller|Shipping API Controller]]
- [[_COMMUNITY_Configuration & Auth|Configuration & Auth]]
- [[_COMMUNITY_Payment Expiry Job|Payment Expiry Job]]
- [[_COMMUNITY_Webhook Retry Job|Webhook Retry Job]]
- [[_COMMUNITY_Webhook Audit Log|Webhook Audit Log]]
- [[_COMMUNITY_XenPlatform DTOs|XenPlatform DTOs]]
- [[_COMMUNITY_Wilayah Region DTOs|Wilayah Region DTOs]]
- [[_COMMUNITY_Provider Extensibility|Provider Extensibility]]
- [[_COMMUNITY_XenPlatform Worker Handler|XenPlatform Worker Handler]]
- [[_COMMUNITY_Claude Golang Migrate|Claude Golang Migrate]]
- [[_COMMUNITY_Common Response|Common Response]]
- [[_COMMUNITY_Internal Worker Tasks Go|Internal Worker Tasks Go]]
- [[_COMMUNITY_Payment Doc Db Schema Transactions|Payment Doc Db Schema Transactions]]
- [[_COMMUNITY_Internal Region Model Go|Internal Region Model Go]]
- [[_COMMUNITY_Payment Doc Payment Service|Payment Doc Payment Service]]
- [[_COMMUNITY_Internal Payment Provider Xendit Signature Go Xendit Provider|Internal Payment Provider Xendit Signature Go Xendit Provider]]
- [[_COMMUNITY_Swagger Shipment Model|Swagger Shipment Model]]
- [[_COMMUNITY_Swagger Shipment Status Enum|Swagger Shipment Status Enum]]
- [[_COMMUNITY_Swagger Create Shipment Request|Swagger Create Shipment Request]]
- [[_COMMUNITY_Payment Doc Overview|Payment Doc Overview]]
- [[_COMMUNITY_Payment Doc Package Structure|Payment Doc Package Structure]]
- [[_COMMUNITY_Payment Doc Db Schema Payouts|Payment Doc Db Schema Payouts]]

## God Nodes (most connected - your core abstractions)
1. `main()` - 28 edges
2. `paths` - 16 edges
3. `Provider` - 14 edges
4. `responses` - 13 edges
5. `responses` - 12 edges
6. `WebhookEventRepository` - 11 edges
7. `definitions` - 10 edges
8. `post` - 9 edges
9. `post` - 9 edges
10. `post` - 9 edges

## Surprising Connections (you probably didn't know these)
- `In-Memory Cache (24h TTL)` --semantically_similar_to--> `payment_idempotency_keys Table`  [INFERRED] [semantically similar]
  README.md → docs/payment-module.md
- `main()` --calls--> `ParseENV()`  [INFERRED]
  http/main.go → config/config.go
- `main()` --calls--> `RegisterPaymentHandlers()`  [INFERRED]
  http/main.go → internal/payment/controller.go
- `main()` --calls--> `RegisterWebhookHandlerV2()`  [INFERRED]
  http/main.go → internal/payment/controller.go
- `main()` --calls--> `NewAsynqHandler()`  [INFERRED]
  http/main.go → internal/payment/webhook/handler.go

## Hyperedges (group relationships)
- **Webhook Processing Safety Triad: SELECT FOR UPDATE + State Machine + Ledger Idempotency** — payment_doc_select_for_update, payment_doc_state_machine, payment_doc_idempotency_pattern [EXTRACTED 1.00]
- **Atomic Payment Mutation: TxRunner + Repository + Ledger Service** — claude_txrunner, payment_doc_repository_layer, payment_doc_ledger_service [EXTRACTED 1.00]
- **Async Webhook Pipeline: HTTP Ingest → Asynq Queue → Processor** — readme_xendit_webhook_endpoint, claude_asynq_worker, payment_doc_webhook_processor [EXTRACTED 1.00]

## Communities (69 total, 25 thin omitted)

### Community 0 - "Swagger Schema Properties"
Cohesion: 0.04
Nodes (48): description, type, type, type, type, type, properties, type (+40 more)

### Community 1 - "Swagger API Responses"
Cohesion: 0.10
Nodes (43): description, schema, description, schema, description, schema, description, schema (+35 more)

### Community 2 - "Swagger Shipment & Courier Fields"
Cohesion: 0.05
Nodes (42): properties, type, type, type, type, type, properties, type (+34 more)

### Community 3 - "Payment Domain & State Machine"
Cohesion: 0.07
Nodes (15): PaymentStatus, PaymentTransaction, controller, isAccountEvent(), isErrNotFound(), mustParseIntHeader(), RegisterPaymentHandlers(), RegisterWebhookHandlerV2() (+7 more)

### Community 4 - "Swagger Payment Fields"
Cohesion: 0.06
Nodes (35): description, type, type, description, type, definitions, common.Response, github_com_dengankarya_connector_internal_payment_domain.PaymentStatus (+27 more)

### Community 5 - "Double-Entry Ledger Domain"
Cohesion: 0.09
Nodes (17): LedgerAccountType, LedgerDirection, LedgerEntry, LedgerJournal, CursorPoint, jsonParam(), marshalJSON(), mustParseUUID() (+9 more)

### Community 6 - "Xendit Provider Integration"
Cohesion: 0.09
Nodes (17): Provider, createPayoutDTO, createRefundDTO, createSessionDTO, createTransferDTO, customerDTO, IndividualDetail, fromSessionResponseDTO() (+9 more)

### Community 7 - "Swagger Shipping Address Fields"
Cohesion: 0.07
Nodes (27): description, type, description, type, github_com_dengankarya_connector_internal_shipping_provider.Address, description, type, properties (+19 more)

### Community 8 - "Swagger Shipping Entities"
Cohesion: 0.07
Nodes (27): allOf, description, allOf, description, allOf, description, properties, description (+19 more)

### Community 9 - "Biteship Shipping Client"
Cohesion: 0.11
Nodes (10): Client, mapCreateOrderResponse(), mapCreateShipmentRequest(), MapStatus(), orderPriceEvent, orderStatusEvent, orderWaybillIDEvent, ShipmentWebhookForwarder (+2 more)

### Community 10 - "Swagger Region Endpoints"
Cohesion: 0.22
Nodes (26): description, description, parameters, produces, responses, security, summary, tags (+18 more)

### Community 11 - "Biteship Data Transfer Objects"
Cohesion: 0.09
Nodes (21): BiteshipDetailedAddress, BiteshipPricingResponse, CashOnDeliveryResponse, CoordinateRequest, CoordinateResponse, Courier, CourierInsuranceResponse, CourierResponse (+13 more)

### Community 12 - "Shipment Domain & Repository"
Cohesion: 0.12
Nodes (8): Shipment, ShipmentStatus, ctxKey, DBTX, nilIfEmptyPtr(), NewShipmentRepository(), scanShipment(), ShipmentRepository

### Community 13 - "Webhook Event Domain"
Cohesion: 0.15
Nodes (6): WebhookEvent, WebhookProcessingStatus, collectEvents(), NewWebhookEventRepository(), scanEvent(), WebhookEventRepository

### Community 14 - "Region Cache & Wilayah Client"
Cohesion: 0.19
Nodes (3): NewCachedClient(), cachedClient, Client

### Community 15 - "Webhook Processing Engine"
Cohesion: 0.21
Nodes (7): parsePaymentDetails(), paymentDetails, Processor, deriveEventID(), extractInvoiceID(), NewProcessor(), WebhookForwarder

### Community 16 - "Payment Service Layer"
Cohesion: 0.16
Nodes (8): CreateManualPaymentRequest, CreatePaymentRequest, cursorPayload, ListTransactionsRequest, ListTransactionsResult, decodeCursor(), encodeCursor(), PaymentService

### Community 17 - "Swagger API Metadata"
Cohesion: 0.14
Nodes (13): basePath, consumes, email, name, url, info, contact, description (+5 more)

### Community 18 - "Payout Domain & Repository"
Cohesion: 0.19
Nodes (7): Payout, PayoutItem, PayoutStatus, collectPayouts(), NewPayoutRepository(), scanPayout(), PayoutRepository

### Community 19 - "Payment Provider Interface"
Cohesion: 0.15
Nodes (12): Balance, BalanceRequest, CreateInvoiceRequest, CreatePayoutRequest, CreateRefundRequest, Invoice, PaymentProvider, Payout (+4 more)

### Community 20 - "HTTP Server & Infra Bootstrap"
Cohesion: 0.21
Nodes (7): ConnectPgx(), registerHealthHandler(), main(), authenticatedRequest(), basicAuth(), parseBasicAuth(), requestLogger()

### Community 21 - "Payment Domain Errors"
Cohesion: 0.18
Nodes (5): ErrAlreadyInState, ErrAmountMismatch, ErrInvalidStatusTransition, ErrLedgerImbalance, ErrNotFound

### Community 22 - "Architecture Documentation"
Cohesion: 0.22
Nodes (10): Asynq Worker, http/main.go (HTTP Server + Asynq Worker), Payment Module (internal/payment/), PostgreSQL via pgx/v5, Webhook Processing Pipeline, payment_webhook_events Table, Observability & Structured Logging (logrus JSON), Repository Layer (internal/payment/repository/) (+2 more)

### Community 23 - "Payment Module Docs"
Cohesion: 0.22
Nodes (10): payment_idempotency_keys Table, payment_ledger_entries Table, Domain Layer (internal/payment/domain/), Double-Entry Ledger, Idempotency Pattern, Ledger Account Types (escrow/merchant_payable/platform_fee/payout/refund), Ledger Service (ledger/service.go), Payment Transaction State Machine (+2 more)

### Community 24 - "Payment DB Repository"
Cohesion: 0.24
Nodes (7): ctxKey, dbFromContext(), WithTx(), DBTX, NewTxRunner(), TxFromContext(), TxRunner

### Community 25 - "Shipping Provider Interface"
Cohesion: 0.20
Nodes (9): Address, CODOptions, CourierSelection, CreateShipmentRequest, DeliveryOptions, ProofOfDeliveryOptions, ShipmentItem, Shipper (+1 more)

### Community 27 - "Idempotency Repository"
Cohesion: 0.28
Nodes (3): isNotFoundError(), IdempotencyRecord, IdempotencyRepository

### Community 28 - "Cache Layer Architecture"
Cohesion: 0.29
Nodes (8): Decorator Cache Pattern, Region Module (internal/region/), Shipping Module (internal/shipping/), Biteship, In-Memory Cache (24h TTL), GET /api/shippings/couriers, POST /webhook/biteship, POST /shipments

### Community 29 - "Region Service"
Cohesion: 0.25
Nodes (3): RegionClient, RegionService, NewRegionService()

### Community 30 - "Payment HTTP Models"
Cohesion: 0.25
Nodes (7): Account, CreateAccountRequest, CreateManualPaymentBody, CreatePaymentBody, PublicProfile, ReplayWebhookBody, WebhookEvent

### Community 32 - "Service Overview"
Cohesion: 0.29
Nodes (7): XenPlatform Sub-Account Routing (for-user-id), Connector Service, DenganKarya Backend, Regions API Endpoints, Tokokarya, Wilayah.id, Xendit

### Community 33 - "Auth & Manual Payment"
Cohesion: 0.29
Nodes (7): Manual Payment Mode, X-Tenant-ID Multi-Tenant Header, X-API-KEY Authentication, POST /webhook/xendit, ApiKeyAuth Security Definition, CreateManualPaymentBody, WebhookEvent Model

### Community 35 - "Asynq Worker Server"
Cohesion: 0.33
Nodes (5): MuxOptions, NewMux(), NewServer(), TokokaryaNotifier, WebhookEventHandler

### Community 36 - "Shipping Service"
Cohesion: 0.33
Nodes (3): LogisticAggregator, NewShippingService(), ShippingService

### Community 40 - "Transaction Safety Patterns"
Cohesion: 0.40
Nodes (5): TxRunner.RunInTx, Payout Service (service/payout.go), Payout System, Security Design, SELECT FOR UPDATE Locking Pattern

### Community 41 - "Webhook Retry & Replay"
Cohesion: 0.40
Nodes (5): Background Jobs (expire_payments + retry_webhooks), Failure Scenarios & Recovery, Replay Service (webhook/replay.go), Webhook Replay & Retry Mechanism, ReplayWebhookBody

### Community 42 - "Shipping Cache Aggregator"
Cohesion: 0.50
Nodes (3): filterCouriers(), NewCachedAggregator(), cachedAggregator

### Community 48 - "XenPlatform DTOs"
Cohesion: 0.50
Nodes (3): accountDTO, createAccountRequestDTO, publicProfileDTO

### Community 49 - "Wilayah Region DTOs"
Cohesion: 0.50
Nodes (3): Area, AreasResponse, Meta

### Community 50 - "Provider Extensibility"
Cohesion: 0.67
Nodes (3): PaymentProvider Interface, Xendit Provider Implementation, Adding a New Payment Provider Guide

## Knowledge Gaps
- **262 isolated node(s):** `XenPlatformClient`, `PublicProfile`, `Account`, `CreateAccountRequest`, `WebhookEvent` (+257 more)
  These have ≤1 connection - possible missing edges or undocumented components.
- **25 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `definitions` connect `Swagger Payment Fields` to `Swagger API Metadata`, `Swagger Shipping Address Fields`?**
  _High betweenness centrality (0.071) - this node is a cross-community bridge._
- **Why does `main()` connect `HTTP Server & Infra Bootstrap` to `Payment Domain & State Machine`, `Asynq Worker Server`, `Double-Entry Ledger Domain`, `Shipping Service`, `Shipping Cache Aggregator`, `Configuration & Auth`, `Webhook Event Domain`, `Shipment Domain & Repository`, `Webhook Audit Log`, `Webhook Processing Engine`, `Payment Expiry Job`, `Payout Domain & Repository`, `Webhook Retry Job`, `Region Cache & Wilayah Client`, `Payment DB Repository`, `Region Service`?**
  _High betweenness centrality (0.066) - this node is a cross-community bridge._
- **Why does `paths` connect `Swagger Region Endpoints` to `Swagger API Metadata`, `Swagger API Responses`?**
  _High betweenness centrality (0.046) - this node is a cross-community bridge._
- **Are the 27 inferred relationships involving `main()` (e.g. with `ParseENV()` and `ConnectPgx()`) actually correct?**
  _`main()` has 27 INFERRED edges - model-reasoned connections that need verification._
- **What connects `XenPlatformClient`, `PublicProfile`, `Account` to the rest of the system?**
  _263 weakly-connected nodes found - possible documentation gaps or missing edges._
- **Should `Swagger Schema Properties` be split into smaller, more focused modules?**
  _Cohesion score 0.041666666666666664 - nodes in this community are weakly interconnected._
- **Should `Swagger API Responses` be split into smaller, more focused modules?**
  _Cohesion score 0.09523809523809523 - nodes in this community are weakly interconnected._