package webhook

import "encoding/json"

// paymentDetails holds the fields we extract from a Xendit payment webhook payload.
type paymentDetails struct {
	PaymentSessionID string // payment_session_id (ps-xxx); primary transaction lookup key
	PaymentID        string // payment_id (py-xxx); stored as ProviderPaymentID on capture
	ChannelCode      string // channel_code, e.g. "QRIS", "BCA"
	FailureCode      string // failure_code; non-empty on payment.failure
}

// parsePaymentDetails extracts key fields from a raw Xendit payment webhook payload.
// Returns zero-value struct on any parse error — callers handle missing fields gracefully.
func parsePaymentDetails(raw []byte) paymentDetails {
	var envelope struct {
		Data struct {
			PaymentSessionID       string   `json:"payment_session_id"`
			PaymentID              string   `json:"payment_id"`
			ChannelCode            string   `json:"channel_code"`
			FailureCode            string   `json:"failure_code"`
			AllowedPaymentChannels []string `json:"allowed_payment_channels"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return paymentDetails{}
	}

	// payment_session.completed does not carry channel_code — fall back to
	// allowed_payment_channels[0] since callers always send exactly one channel.
	channelCode := envelope.Data.ChannelCode
	if channelCode == "" && len(envelope.Data.AllowedPaymentChannels) == 1 {
		channelCode = envelope.Data.AllowedPaymentChannels[0]
	}

	return paymentDetails{
		PaymentSessionID: envelope.Data.PaymentSessionID,
		PaymentID:        envelope.Data.PaymentID,
		ChannelCode:      channelCode,
		FailureCode:      envelope.Data.FailureCode,
	}
}

// midtransPaymentDetails holds key fields extracted from a Midtrans webhook notification.
type midtransPaymentDetails struct {
	OrderID           string // order_id — primary transaction lookup key
	TransactionID     string // transaction_id from Midtrans
	PaymentType       string // payment_type, e.g. "bank_transfer", "gopay", "qris"
	TransactionStatus string // transaction_status, e.g. "settlement", "pending"
	FailureCode       string // status_code when the transaction failed
}

// parseMidtransPaymentDetails extracts key fields from a raw Midtrans webhook notification.
// Returns zero-value struct on any parse error — callers handle missing fields gracefully.
func parseMidtransPaymentDetails(raw []byte) midtransPaymentDetails {
	var n struct {
		OrderID           string `json:"order_id"`
		TransactionID     string `json:"transaction_id"`
		PaymentType       string `json:"payment_type"`
		TransactionStatus string `json:"transaction_status"`
		StatusCode        string `json:"status_code"`
	}
	if err := json.Unmarshal(raw, &n); err != nil {
		return midtransPaymentDetails{}
	}

	var failureCode string
	if n.TransactionStatus == "deny" || n.TransactionStatus == "cancel" || n.TransactionStatus == "failure" {
		failureCode = n.StatusCode
	}

	return midtransPaymentDetails{
		OrderID:           n.OrderID,
		TransactionID:     n.TransactionID,
		PaymentType:       n.PaymentType,
		TransactionStatus: n.TransactionStatus,
		FailureCode:       failureCode,
	}
}

