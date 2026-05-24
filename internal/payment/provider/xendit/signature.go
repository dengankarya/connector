// Package xendit implements the PaymentProvider interface for Xendit.
package xendit

import (
	"context"
	"crypto/hmac"

	"github.com/dengankarya/overwatch/internal/payment/domain"
)

// ValidateWebhookSignature checks the x-callback-token header against the configured secret.
// Xendit uses a static shared token (not HMAC) — constant-time comparison prevents timing attacks.
func (p *Provider) ValidateWebhookSignature(_ context.Context, _ []byte, headers map[string]string) error {
	if p.webhookToken == "" {
		// No token configured — skip validation (development only, not safe in production).
		return nil
	}

	token := headers["x-callback-token"]
	if token == "" {
		return domain.ErrMissingSignature
	}

	// Use hmac.Equal for constant-time comparison to prevent timing side-channels.
	if !hmac.Equal([]byte(token), []byte(p.webhookToken)) {
		return domain.ErrInvalidSignature
	}
	return nil
}
