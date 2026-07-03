package account

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/sirupsen/logrus"
)

// XenditAccountWebhookHandler handles Xendit account-level webhook events
// (account.*, account_holder.*). It is separate from the payment webhook pipeline.
type XenditAccountWebhookHandler struct {
	repo   *Repository
	logger *logrus.Logger
}

// NewXenditAccountWebhookHandler creates a handler for account-level Xendit webhooks.
func NewXenditAccountWebhookHandler(repo *Repository, logger *logrus.Logger) *XenditAccountWebhookHandler {
	return &XenditAccountWebhookHandler{repo: repo, logger: logger}
}

// IsAccountEvent reports whether an event type string is an account-level Xendit event.
// Satisfies the webhook.AccountEventHandler interface.
func (h *XenditAccountWebhookHandler) IsAccountEvent(eventType string) bool {
	return strings.HasPrefix(eventType, "account.") || strings.HasPrefix(eventType, "account_holder.")
}

// Handle dispatches an account webhook event to the appropriate handler.
// Always returns 200-safe — errors are logged, not propagated.
func (h *XenditAccountWebhookHandler) Handle(ctx context.Context, eventType string, rawPayload []byte) {
	log := h.logger.WithFields(logrus.Fields{
		"component":  "xendit_account_webhook",
		"event_type": eventType,
	})

	switch eventType {
	case "account.registered", "account.activated":
		h.handleAccountStatus(ctx, eventType, rawPayload, log)
	case "account.suspected", "account.suspended", "account.cleared":
		h.handleAccountSuspension(ctx, rawPayload, log)
	case "account_holder.kyc.status":
		h.handleKYCStatus(rawPayload, log)
	case "account_holder.capabilities.status":
		h.handleCapabilitiesStatus(rawPayload, log)
	default:
		log.Warn("unrecognised xendit account event — ignoring")
	}
}

// ── payload types ─────────────────────────────────────────────────────────────

type accountStatusPayload struct {
	Event string `json:"event"`
	Data  struct {
		UserID      string `json:"user_id"`
		AccountInfo struct {
			PaymentsEnabled bool `json:"payments_enabled"`
		} `json:"account_info"`
	} `json:"data"`
}

type accountSuspensionPayload struct {
	Event string `json:"event"`
	Data  struct {
		ID     string `json:"id"`
		Status string `json:"status"` // SUSPECTED | SUSPENDED | CLEARED
		Reason string `json:"reason"`
	} `json:"data"`
}

type accountHolderKYCPayload struct {
	Event      string `json:"event"`
	BusinessID string `json:"business_id"`
	Data       struct {
		ID  string `json:"id"`
		KYC struct {
			Status      string `json:"status"` // PASSED | FAILED | RESUBMISSION_REQUIRED
			VerifiedAt  string `json:"verified_at"`
			KYCPassedAt string `json:"kyc_passed_at"`
		} `json:"kyc"`
	} `json:"data"`
}

type accountHolderCapabilitiesPayload struct {
	Event      string `json:"event"`
	BusinessID string `json:"business_id"`
	Data       struct {
		ID           string `json:"id"`
		Capabilities []struct {
			Type        string `json:"type"`
			ChannelCode string `json:"channel_code"`
			Status      string `json:"status"`
		} `json:"capabilities"`
	} `json:"data"`
}

// ── handlers ──────────────────────────────────────────────────────────────────

func (h *XenditAccountWebhookHandler) handleAccountStatus(ctx context.Context, eventType string, raw []byte, log *logrus.Entry) {
	var p accountStatusPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		log.WithError(err).Error("failed to parse account status payload")
		return
	}
	if p.Data.UserID == "" {
		log.Warn("account status webhook missing data.user_id")
		return
	}

	// Map Xendit event → our status string.
	status := "REGISTERED"
	if eventType == "account.activated" {
		status = "LIVE"
	}

	if err := h.repo.UpdateSubAccountStatusByGatewayID(ctx, "xendit", p.Data.UserID, status); err != nil {
		log.WithError(err).Error("failed to update sub-account status")
		return
	}

	log.WithFields(logrus.Fields{
		"gateway_account_id": p.Data.UserID,
		"new_status":         status,
		"payments_enabled":   p.Data.AccountInfo.PaymentsEnabled,
	}).Info("sub-account status updated")
}

func (h *XenditAccountWebhookHandler) handleAccountSuspension(ctx context.Context, raw []byte, log *logrus.Entry) {
	var p accountSuspensionPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		log.WithError(err).Error("failed to parse account suspension payload")
		return
	}
	if p.Data.ID == "" {
		log.Warn("account suspension webhook missing data.id")
		return
	}

	if err := h.repo.UpdateSubAccountStatusByGatewayID(ctx, "xendit", p.Data.ID, p.Data.Status); err != nil {
		log.WithError(err).Error("failed to update sub-account status after suspension event")
		return
	}

	log.WithFields(logrus.Fields{
		"gateway_account_id": p.Data.ID,
		"new_status":         p.Data.Status,
		"reason":             p.Data.Reason,
	}).Warn("sub-account suspension status updated")
}

func (h *XenditAccountWebhookHandler) handleKYCStatus(raw []byte, log *logrus.Entry) {
	var p accountHolderKYCPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		log.WithError(err).Error("failed to parse account holder KYC payload")
		return
	}
	log.WithFields(logrus.Fields{
		"account_holder_id": p.Data.ID,
		"kyc_status":        p.Data.KYC.Status,
		"business_id":       p.BusinessID,
	}).Info("account holder KYC status received")
	// No DB update needed yet — the sub-account status progresses via account.activated event.
}

func (h *XenditAccountWebhookHandler) handleCapabilitiesStatus(raw []byte, log *logrus.Entry) {
	var p accountHolderCapabilitiesPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		log.WithError(err).Error("failed to parse account holder capabilities payload")
		return
	}
	log.WithFields(logrus.Fields{
		"account_holder_id": p.Data.ID,
		"capabilities":      p.Data.Capabilities,
		"business_id":       p.BusinessID,
	}).Info("account holder capabilities status received")
}
