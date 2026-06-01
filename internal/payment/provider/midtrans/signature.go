// Package midtrans implements the PaymentProvider interface for Midtrans SNAP.
package midtrans

import (
	"context"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/dengankarya/connector/internal/payment/domain"
)

// ValidateWebhookSignature verifies the Midtrans webhook notification signature.
//
// Midtrans includes a `signature_key` field inside the JSON payload (not in headers).
// The expected signature is: SHA512(order_id + status_code + gross_amount + ServerKey)
//
// See: https://docs.midtrans.com/docs/https-notification-webhooks
func (p *Provider) ValidateWebhookSignature(_ context.Context, payload []byte, _ map[string]string) error {
	if p.serverKey == "" {
		// No key configured — skip validation (development only).
		return nil
	}

	// Extract the fields needed for verification from the payload.
	var fields struct {
		OrderID      string `json:"order_id"`
		StatusCode   string `json:"status_code"`
		GrossAmount  string `json:"gross_amount"`
		SignatureKey string `json:"signature_key"`
	}
	if err := json.Unmarshal(payload, &fields); err != nil {
		return fmt.Errorf("midtrans: unmarshal signature fields: %w", err)
	}

	if fields.SignatureKey == "" {
		return domain.ErrMissingSignature
	}

	// Compute expected signature: SHA512(order_id + status_code + gross_amount + ServerKey)
	input := fields.OrderID + fields.StatusCode + fields.GrossAmount + p.serverKey
	hash := sha512.Sum512([]byte(input))
	expected := hex.EncodeToString(hash[:])

	// Constant-time comparison to prevent timing side-channels.
	if subtle.ConstantTimeCompare([]byte(expected), []byte(fields.SignatureKey)) != 1 {
		return domain.ErrInvalidSignature
	}

	return nil
}
