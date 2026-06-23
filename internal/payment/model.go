package payment

import "time"

// CreatePaymentBody is the request body for POST /payments.
type CreatePaymentBody struct {
	OrderNumber            string            `json:"order_number"`
	IdempotencyKey         string            `json:"idempotency_key"`
	Amount                 int64             `json:"amount"`
	Currency               string            `json:"currency"`
	PlatformFee            int64             `json:"platform_fee"`
	ShippingFee            int64             `json:"shipping_fee"`
	SuccessReturnURL       string            `json:"success_return_url"`
	CancelReturnURL        string            `json:"cancel_return_url"`
	Description            string            `json:"description"`
	CustomerName           string            `json:"customer_name"`
	CustomerEmail          string            `json:"customer_email"`
	CustomerMobile         string            `json:"customer_mobile"`
	ExpiresAt              *time.Time        `json:"expires_at"`
	Metadata               map[string]string `json:"metadata"`
	PaymentType            string            `json:"payment_type"`
	PaymentMethodTypes     []string          `json:"payment_method_types"`
	WebhookNotificationURL string            `json:"webhook_notification_url"` // override webhook URL
	ResultURL              string            `json:"result_url"`               // result page URL for DOKU
}

// CreateManualPaymentBody is the request body for POST /payments/manual.
type CreateManualPaymentBody struct {
	OrderNumber    string            `json:"order_number"`
	IdempotencyKey string            `json:"idempotency_key"`
	Amount         int64             `json:"amount"`
	Currency       string            `json:"currency"`
	PlatformFee    int64             `json:"platform_fee"`
	ShippingFee    int64             `json:"shipping_fee"`
	PaymentMethod  string            `json:"payment_method"`
	PaymentChannel string            `json:"payment_channel"`
	Description    string            `json:"description"`
	Metadata       map[string]string `json:"metadata"`
}

// ReplayWebhookBody is the request body for POST /payments/webhooks/{event_id}/replay.
type ReplayWebhookBody struct {
	Force bool `json:"force"`
}

// ConfirmManualPaymentBody is the request body for POST /payments/manual/{id}/confirm.
type ConfirmManualPaymentBody struct {
	PaymentChannel string `json:"payment_channel"`
}

// ScheduleOrderCancellationBody is the request body for POST /payments/cancel-schedule.
type ScheduleOrderCancellationBody struct {
	OrderNumber    string `json:"order_number"`
	ShouldExpireAt int64  `json:"should_expired_at"` // Unix timestamp
}
