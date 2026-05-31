package payment

// CreatePaymentBody is the request body for POST /payments.
type CreatePaymentBody struct {
	OrderNumber            string         `json:"order_number"`
	IdempotencyKey         string         `json:"idempotency_key"`
	Amount                 int64          `json:"amount"`
	Currency               string         `json:"currency"`
	PlatformFee            int64          `json:"platform_fee"`
	AllowedPaymentChannels []string       `json:"allowed_payment_channels"`
	SuccessReturnURL       string         `json:"success_return_url"`
	CancelReturnURL        string         `json:"cancel_return_url"`
	Description            string         `json:"description"`
	CustomerEmail          string         `json:"customer_email"`
	CustomerName           string         `json:"customer_name"`
	CustomerReferenceID    string         `json:"customer_reference_id"`
	Metadata               map[string]any `json:"metadata"`
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
