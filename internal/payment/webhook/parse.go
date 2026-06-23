package webhook

import (
	"encoding/json"
)

// paymentDetails holds the fields we extract from a payment webhook payload.
type paymentDetails struct {
	PaymentSessionID string // primary transaction lookup key (Xendit: payment_session_id; DOKU: order.invoice_number)
	PaymentID        string // secondary payment identifier (Xendit: payment_id; DOKU: transaction.original_request_id)
	ChannelCode      string // payment channel (Xendit: channel_code; DOKU: channel.id)
	FailureCode      string // non-empty on failure events (Xendit only)
}

// parsePaymentDetails extracts key fields from a raw payment webhook payload.
// It handles both Xendit (data-wrapped) and DOKU (flat) notification formats.
// Returns zero-value struct on any parse error — callers handle missing fields gracefully.
func parsePaymentDetails(raw []byte) paymentDetails {
	// Xendit envelope: all fields live under a "data" key.
	var xendit struct {
		Data struct {
			PaymentSessionID       string   `json:"payment_session_id"`
			PaymentID              string   `json:"payment_id"`
			ChannelCode            string   `json:"channel_code"`
			FailureCode            string   `json:"failure_code"`
			AllowedPaymentChannels []string `json:"allowed_payment_channels"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &xendit); err == nil && xendit.Data.PaymentSessionID != "" {
		channelCode := xendit.Data.ChannelCode
		if channelCode == "" && len(xendit.Data.AllowedPaymentChannels) == 1 {
			channelCode = xendit.Data.AllowedPaymentChannels[0]
		}
		return paymentDetails{
			PaymentSessionID: xendit.Data.PaymentSessionID,
			PaymentID:        xendit.Data.PaymentID,
			ChannelCode:      channelCode,
			FailureCode:      xendit.Data.FailureCode,
		}
	}

	// DOKU notification: flat structure with order.invoice_number and channel.id.
	var doku struct {
		Order struct {
			InvoiceNumber string `json:"invoice_number"`
		} `json:"order"`
		Channel struct {
			ID string `json:"id"`
		} `json:"channel"`
		Transaction struct {
			OriginalRequestID string `json:"original_request_id"`
		} `json:"transaction"`
	}
	if err := json.Unmarshal(raw, &doku); err == nil && doku.Order.InvoiceNumber != "" {
		return paymentDetails{
			PaymentSessionID: doku.Order.InvoiceNumber,
			PaymentID:        doku.Transaction.OriginalRequestID,
			ChannelCode:      doku.Channel.ID,
		}
	}

	return paymentDetails{}
}
