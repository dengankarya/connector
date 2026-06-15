package domain

import (
	"time"

	"github.com/google/uuid"
)

// WebhookProcessingStatus tracks where a raw webhook event is in the processing pipeline.
type WebhookProcessingStatus string

const (
	WebhookStatusReceived     WebhookProcessingStatus = "received"
	WebhookStatusProcessing   WebhookProcessingStatus = "processing"
	WebhookStatusProcessed    WebhookProcessingStatus = "processed"
	WebhookStatusFailed       WebhookProcessingStatus = "failed"
	WebhookStatusDeadLettered WebhookProcessingStatus = "dead_lettered"
)

// WebhookEvent stores a raw inbound webhook payload for audit, replay, and debugging.
// It is append-only: once received, only ProcessingStatus, LastError, and ProcessedAt change.
type WebhookEvent struct {
	ID                 uuid.UUID               `json:"id,omitempty"`
	Provider           string                  `json:"provider,omitempty"`          // "xendit", "midtrans", etc.
	ProviderEventID    string                  `json:"provider_event_id,omitempty"` // provider's unique identifier for this event
	EventType          string                  `json:"event_type,omitempty"`        // e.g. "invoice.paid", "invoice.expired"
	RawPayload         []byte                  `json:"raw_payload,omitempty"`       // unmodified request body — preserved for replay
	Headers            map[string]string       `json:"headers,omitempty"`
	Signature          string                  `json:"signature,omitempty"`
	SignatureValid     bool                    `json:"signature_valid,omitempty"`
	ProcessingStatus   WebhookProcessingStatus `json:"processing_status,omitempty"`
	ProcessingAttempts int                     `json:"processing_attempts,omitempty"`
	LastError          string                  `json:"last_error,omitempty"`
	TransactionID      *uuid.UUID              `json:"transaction_id,omitempty"` // set after successful processing
	ProcessedAt        *time.Time              `json:"processed_at,omitempty"`
	CreatedAt          time.Time               `json:"created_at"`
}
