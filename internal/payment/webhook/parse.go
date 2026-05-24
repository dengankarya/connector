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
			PaymentSessionID string `json:"payment_session_id"`
			PaymentID        string `json:"payment_id"`
			ChannelCode      string `json:"channel_code"`
			FailureCode      string `json:"failure_code"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return paymentDetails{}
	}
	return paymentDetails{
		PaymentSessionID: envelope.Data.PaymentSessionID,
		PaymentID:        envelope.Data.PaymentID,
		ChannelCode:      envelope.Data.ChannelCode,
		FailureCode:      envelope.Data.FailureCode,
	}
}
