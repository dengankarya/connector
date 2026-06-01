package midtrans

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/dengankarya/connector/internal/payment/provider"
)

// ─── outbound DTOs (Connector → Midtrans SNAP) ──────────────────────────────

// snapCreateTransactionDTO maps to POST /snap/v1/transactions.
type snapCreateTransactionDTO struct {
	TransactionDetails snapTransactionDetailsDTO `json:"transaction_details"`
	CustomerDetails    *snapCustomerDetailsDTO   `json:"customer_details,omitempty"`
	EnabledPayments    []string                  `json:"enabled_payments,omitempty"`
	Callbacks          *snapCallbacksDTO         `json:"callbacks,omitempty"`
	CustomExpiry       *snapCustomExpiryDTO      `json:"custom_expiry,omitempty"`
	Metadata           map[string]string         `json:"metadata,omitempty"`
}

type snapTransactionDetailsDTO struct {
	OrderID  string `json:"order_id"`
	GrossAmt int64  `json:"gross_amount"`
}

type snapCustomerDetailsDTO struct {
	FirstName string `json:"first_name,omitempty"`
	Email     string `json:"email,omitempty"`
}

type snapCallbacksDTO struct {
	Finish string `json:"finish,omitempty"`
}

type snapCustomExpiryDTO struct {
	ExpiryDuration int    `json:"expiry_duration"`
	Unit           string `json:"unit"` // "minute", "hour", "day"
}

// ─── inbound DTOs (Midtrans → Connector) ─────────────────────────────────────

// snapResponseDTO is the response from POST /snap/v1/transactions.
type snapResponseDTO struct {
	Token       string `json:"token"`
	RedirectURL string `json:"redirect_url"`
}

// statusResponseDTO maps the response from GET /v2/{order_id}/status.
type statusResponseDTO struct {
	StatusCode        string `json:"status_code"`
	StatusMessage     string `json:"status_message"`
	TransactionID     string `json:"transaction_id"`
	OrderID           string `json:"order_id"`
	GrossAmount       string `json:"gross_amount"`
	PaymentType       string `json:"payment_type"`
	TransactionStatus string `json:"transaction_status"`
	FraudStatus       string `json:"fraud_status"`
	TransactionTime   string `json:"transaction_time"`
	SettlementTime    string `json:"settlement_time"`
	ExpiryTime        string `json:"expiry_time"`
	SignatureKey      string `json:"signature_key"`
}

// webhookNotificationDTO is the flat JSON payload Midtrans sends to the webhook endpoint.
type webhookNotificationDTO struct {
	TransactionTime   string `json:"transaction_time"`
	TransactionStatus string `json:"transaction_status"`
	TransactionID     string `json:"transaction_id"`
	StatusMessage     string `json:"status_message"`
	StatusCode        string `json:"status_code"`
	SignatureKey      string `json:"signature_key"`
	SettlementTime    string `json:"settlement_time"`
	PaymentType       string `json:"payment_type"`
	OrderID           string `json:"order_id"`
	MerchantID        string `json:"merchant_id"`
	GrossAmount       string `json:"gross_amount"`
	FraudStatus       string `json:"fraud_status"`
	Currency          string `json:"currency"`
	ChannelResponseCode    string `json:"channel_response_code"`
	ChannelResponseMessage string `json:"channel_response_message"`
}

// ─── mappers ─────────────────────────────────────────────────────────────────

func toSnapCreateDTO(req provider.CreateInvoiceRequest) snapCreateTransactionDTO {
	dto := snapCreateTransactionDTO{
		TransactionDetails: snapTransactionDetailsDTO{
			OrderID:  req.ExternalID,
			GrossAmt: req.Amount,
		},
		EnabledPayments: req.AllowedPaymentChannels,
		Metadata:        req.Metadata,
	}

	if req.CustomerEmail != "" || req.CustomerName != "" {
		dto.CustomerDetails = &snapCustomerDetailsDTO{
			FirstName: req.CustomerName,
			Email:     req.CustomerEmail,
		}
	}

	if req.SuccessReturnURL != "" {
		dto.Callbacks = &snapCallbacksDTO{
			Finish: req.SuccessReturnURL,
		}
	}

	if req.ExpiresAt != nil {
		// Calculate duration from now to the expiry time in minutes.
		dur := time.Until(*req.ExpiresAt)
		if dur > 0 {
			dto.CustomExpiry = &snapCustomExpiryDTO{
				ExpiryDuration: int(dur.Minutes()),
				Unit:           "minute",
			}
		}
	}

	return dto
}

func fromStatusResponseDTO(dto statusResponseDTO) *provider.Invoice {
	inv := &provider.Invoice{
		ProviderInvoiceID: dto.OrderID,
		Status:            mapTransactionStatusToInvoiceStatus(dto.TransactionStatus),
		Currency:          "IDR", // Midtrans only supports IDR
	}

	// Parse gross_amount from string (Midtrans returns "100000.00").
	if amt, err := parseGrossAmount(dto.GrossAmount); err == nil {
		inv.Amount = amt
	}

	// Parse expiry_time if present.
	if dto.ExpiryTime != "" {
		if t, err := parseMidtransTime(dto.ExpiryTime); err == nil {
			inv.ExpiresAt = &t
		}
	}

	return inv
}

func parseWebhookNotification(raw []byte) (*provider.WebhookEvent, error) {
	var n webhookNotificationDTO
	if err := json.Unmarshal(raw, &n); err != nil {
		return nil, fmt.Errorf("parse midtrans webhook notification: %w", err)
	}

	eventType := mapTransactionStatusToEventType(n.TransactionStatus)

	evt := &provider.WebhookEvent{
		ProviderEventID:   n.TransactionID,
		EventType:         eventType,
		ProviderInvoiceID: n.OrderID,
		PaymentID:         n.TransactionID,
		Currency:          n.Currency,
		PaymentMethod:     n.PaymentType,
		PaymentChannel:    n.PaymentType,
		ChannelCode:       n.PaymentType,
		RawPayload:        raw,
	}

	// Parse gross_amount.
	if amt, err := parseGrossAmount(n.GrossAmount); err == nil {
		evt.Amount = amt
	}

	// Parse settlement_time or transaction_time as PaidAt.
	ts := n.SettlementTime
	if ts == "" {
		ts = n.TransactionTime
	}
	if ts != "" {
		if t, err := parseMidtransTime(ts); err == nil {
			evt.PaidAt = &t
		}
	}

	// Derive failure code from status_code when transaction failed.
	if n.TransactionStatus == "deny" || n.TransactionStatus == "cancel" || n.TransactionStatus == "failure" {
		evt.FailureCode = n.StatusCode
	}

	return evt, nil
}

// ─── status mapping ──────────────────────────────────────────────────────────

// mapTransactionStatusToEventType normalises Midtrans transaction_status to
// event type strings compatible with the webhook processor's handle() switch.
func mapTransactionStatusToEventType(status string) string {
	switch strings.ToLower(status) {
	case "capture", "settlement":
		return "payment.capture"
	case "pending":
		return "payment.pending"
	case "deny", "cancel", "failure":
		return "payment.failure"
	case "expire":
		return "payment.expire"
	case "refund", "partial_refund":
		return "payment.refund"
	default:
		return "midtrans." + status
	}
}

// mapTransactionStatusToInvoiceStatus maps Midtrans transaction_status to
// the invoice status strings used by RefreshPayment in the service layer.
func mapTransactionStatusToInvoiceStatus(status string) string {
	switch strings.ToLower(status) {
	case "capture", "settlement":
		return "COMPLETED"
	case "pending", "authorize":
		return "PENDING"
	case "expire":
		return "EXPIRED"
	case "cancel":
		return "CANCELLED"
	case "deny", "failure":
		return "FAILED"
	case "refund", "partial_refund":
		return "REFUNDED"
	default:
		return strings.ToUpper(status)
	}
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// parseGrossAmount converts Midtrans gross_amount string (e.g. "100000.00") to int64.
func parseGrossAmount(s string) (int64, error) {
	// Remove decimal part — Midtrans always sends ".00" for IDR.
	if idx := strings.Index(s, "."); idx >= 0 {
		s = s[:idx]
	}
	return strconv.ParseInt(s, 10, 64)
}

// parseMidtransTime parses Midtrans timestamp format "2023-01-01 12:00:00".
func parseMidtransTime(s string) (time.Time, error) {
	// Midtrans uses WIB (UTC+7) but doesn't include timezone in the string.
	loc := time.FixedZone("WIB", 7*60*60)
	return time.ParseInLocation("2006-01-02 15:04:05", s, loc)
}
