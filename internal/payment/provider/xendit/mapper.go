package xendit

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/dengankarya/overwatch/internal/payment/provider"
)

// ─── outbound DTOs (Overwatch → Xendit) ──────────────────────────────────────

// createSessionDTO maps to POST /sessions.
type createSessionDTO struct {
	ReferenceID            string         `json:"reference_id"`
	Currency               string         `json:"currency"`
	Amount                 int64          `json:"amount"`
	Country                string         `json:"country"`
	SessionType            string         `json:"session_type"`             // always "PAY"
	Mode                   string         `json:"mode"`                     // always "PAYMENT_LINK"
	CaptureMethod          string         `json:"capture_method,omitempty"` // "AUTOMATIC"
	AllowedPaymentChannels []string       `json:"allowed_payment_channels,omitempty"`
	Description            string         `json:"description,omitempty"`
	Customer               *customerDTO   `json:"customer,omitempty"`
	ExpiresAt              *time.Time     `json:"expires_at,omitempty"`
	SuccessReturnURL       string         `json:"success_return_url,omitempty"`
	CancelReturnURL        string         `json:"cancel_return_url,omitempty"`
	Metadata               map[string]any `json:"metadata,omitempty"`
}

type customerDTO struct {
	ReferenceID      string           `json:"reference_id"`
	Type             string           `json:"type,omitempty"` // "INDIVIDUAL"
	Email            string           `json:"email,omitempty"`
	IndividualDetail IndividualDetail `json:"individual_detail,omitempty"`
}

type IndividualDetail struct {
	GivenName string `json:"given_names,omitempty"`
}

type createPayoutDTO struct {
	ExternalID    string `json:"external_id"`
	Amount        int64  `json:"amount"`
	BankCode      string `json:"bank_code"`
	AccountNumber string `json:"account_holder_number"`
	AccountName   string `json:"account_holder_name"`
	Description   string `json:"description,omitempty"`
}

type createRefundDTO struct {
	PaymentRequestID string `json:"payment_request_id"`
	Amount           int64  `json:"amount"`
	Reason           string `json:"reason,omitempty"`
	ReferenceID      string `json:"reference_id,omitempty"`
}

type createTransferDTO struct {
	Reference         string `json:"reference"`
	Amount            int64  `json:"amount"`
	DestinationUserID string `json:"destination_user_id"`
}

type transferResponseDTO struct {
	TransferID string `json:"transfer_id"`
	Status     string `json:"status"`
}

// ─── inbound DTOs (Xendit → Overwatch) ───────────────────────────────────────

// sessionResponseDTO maps the response from POST /sessions and GET /sessions/{id}.
type sessionResponseDTO struct {
	PaymentSessionID string     `json:"payment_session_id"`
	ReferenceID      string     `json:"reference_id"`
	Status           string     `json:"status"`
	Currency         string     `json:"currency"`
	Amount           float64    `json:"amount"`
	PaymentLinkURL   string     `json:"payment_link_url"`
	ExpiresAt        *time.Time `json:"expires_at"`
	Created          string     `json:"created"`
}

type payoutResponseDTO struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Amount int64  `json:"amount"`
}

type refundResponseDTO struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Amount int64  `json:"amount"`
}

// webhookPayloadDTO is the outer envelope for all Xendit webhook callbacks.
type webhookPayloadDTO struct {
	Event      string          `json:"event"`
	BusinessID string          `json:"business_id"`
	Created    string          `json:"created"`
	Data       json.RawMessage `json:"data"`
}

// paymentWebhookDataDTO is the inner data for payment.* webhook events.
// The session creates an underlying payment request; both IDs are present.
type paymentWebhookDataDTO struct {
	PaymentID        string  `json:"payment_id"`
	PaymentRequestID string  `json:"payment_request_id"`
	PaymentSessionID string  `json:"payment_session_id"`
	ReferenceID      string  `json:"reference_id"`
	Status           string  `json:"status"`
	FailureCode      string  `json:"failure_code"`
	ChannelCode      string  `json:"channel_code"`
	Country          string  `json:"country"`
	Currency         string  `json:"currency"`
	RequestAmount    float64 `json:"request_amount"`
	Created          string  `json:"created"`
	Updated          string  `json:"updated"`
}

// ─── mappers ─────────────────────────────────────────────────────────────────

func toCreateSessionDTO(req provider.CreateInvoiceRequest) createSessionDTO {
	country := req.Country
	if country == "" {
		country = "ID"
	}

	dto := createSessionDTO{
		ReferenceID:            req.ExternalID,
		Currency:               req.Currency,
		Amount:                 req.Amount,
		Country:                country,
		SessionType:            "PAY",
		Mode:                   "PAYMENT_LINK",
		CaptureMethod:          "AUTOMATIC",
		AllowedPaymentChannels: req.AllowedPaymentChannels,
		Description:            req.Description,
		ExpiresAt:              req.ExpiresAt,
		SuccessReturnURL:       req.SuccessReturnURL,
		CancelReturnURL:        req.CancelReturnURL,
		Metadata:               req.Metadata,
	}

	if req.CustomerEmail != "" && req.CustomerName != "" && req.CustomerReferenceID != "" {
		dto.Customer = &customerDTO{
			ReferenceID: req.CustomerReferenceID,
			Type:        "INDIVIDUAL",
			Email:       req.CustomerEmail,
			IndividualDetail: IndividualDetail{
				GivenName: req.CustomerName,
			},
		}
	}

	return dto
}

func fromSessionResponseDTO(dto sessionResponseDTO) *provider.Invoice {
	return &provider.Invoice{
		ProviderInvoiceID: dto.PaymentSessionID,
		CheckoutURL:       dto.PaymentLinkURL,
		Status:            dto.Status,
		Amount:            int64(dto.Amount),
		Currency:          dto.Currency,
		ExpiresAt:         dto.ExpiresAt,
	}
}

func parseWebhookPayload(raw []byte) (*provider.WebhookEvent, error) {
	var envelope webhookPayloadDTO
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("parse xendit webhook envelope: %w", err)
	}

	eventType := normaliseEventType(envelope.Event)

	var data paymentWebhookDataDTO
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		return nil, fmt.Errorf("parse xendit payment webhook data: %w", err)
	}

	// Use payment_session_id as the primary invoice reference (matches what we store).
	// Fall back to payment_request_id for webhooks that may not carry a session ID.
	providerInvoiceID := data.PaymentSessionID
	if providerInvoiceID == "" {
		providerInvoiceID = data.PaymentRequestID
	}

	evt := &provider.WebhookEvent{
		EventType:         eventType,
		ProviderInvoiceID: providerInvoiceID,
		PaymentID:         data.PaymentID,
		Amount:            int64(data.RequestAmount),
		Currency:          data.Currency,
		ChannelCode:       data.ChannelCode,
		PaymentMethod:     data.ChannelCode,
		PaymentChannel:    data.ChannelCode,
		FailureCode:       data.FailureCode,
		RawPayload:        raw,
	}

	// Use updated timestamp as PaidAt when available.
	ts := data.Updated
	if ts == "" {
		ts = data.Created
	}
	if ts != "" {
		if t, err := time.Parse(time.RFC3339, ts); err == nil {
			evt.PaidAt = &t
		}
	}

	return evt, nil
}

func normaliseEventType(event string) string {
	if event == "" {
		return "unknown"
	}
	return event
}
