package xendit

import "time"

// ── Sub-account ───────────────────────────────────────────────────────────────

type createAccountRequest struct {
	Email         string `json:"email"`
	Type          string `json:"type"` // "OWNED" | "MANAGED"
	PublicProfile struct {
		BusinessName string `json:"business_name"`
	} `json:"public_profile"`
}

type createAccountResponse struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	// Status reflects the account lifecycle: INVITED → REGISTERED → AWAITING_DOCS → PENDING_VERIFICATION → LIVE
	Status string `json:"status"`
}

// AccountResponse is returned by GetAccount.
type AccountResponse struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	Email  string `json:"email"`
	Status string `json:"status"`
	// PublicProfile contains business name and country.
	PublicProfile struct {
		Name    string `json:"name"`
		Country string `json:"country"`
	} `json:"public_profile"`
}

// ── Webhook registration ──────────────────────────────────────────────────────

type setCallbackURLRequest struct {
	URL string `json:"url"`
}

// ── Payment Session ───────────────────────────────────────────────────────────

type createSessionRequest struct {
	ReferenceID            string            `json:"reference_id"`
	Currency               string            `json:"currency"`
	Amount                 float64           `json:"amount"`
	Country                string            `json:"country"`
	SessionType            string            `json:"session_type"`
	Mode                   string            `json:"mode"`
	Description            string            `json:"description,omitempty"`
	AllowedPaymentChannels []string          `json:"allowed_payment_channels,omitempty"`
	ExpiresAt              *time.Time        `json:"expires_at,omitempty"`
	SuccessReturnURL       string            `json:"success_return_url,omitempty"`
	CancelReturnURL        string            `json:"cancel_return_url,omitempty"`
	Metadata               map[string]string `json:"metadata,omitempty"`
	ChannelProperties      map[string]any    `json:"channel_properties,omitempty"`
}

type createSessionResponse struct {
	PaymentSessionID string  `json:"payment_session_id"`
	Status           string  `json:"status"`
	PaymentLinkURL   string  `json:"payment_link_url"`
	ExpiresAt        string  `json:"expires_at"`
	Amount           float64 `json:"amount"`
	Currency         string  `json:"currency"`
}

// ── Session webhook ───────────────────────────────────────────────────────────

// sessionWebhookPayload is the JSON body Xendit sends for Payment Session events.
// Fires for status COMPLETED and EXPIRED.
type sessionWebhookPayload struct {
	Event      string `json:"event"`
	BusinessID string `json:"business_id"`
	Created    string `json:"created"`
	Data       struct {
		PaymentSessionID string  `json:"payment_session_id"`
		ReferenceID      string  `json:"reference_id"`
		Status           string  `json:"status"` // "COMPLETED" | "EXPIRED"
		Amount           float64 `json:"amount"`
		Currency         string  `json:"currency"`
		PaymentID        string  `json:"payment_id"` // populated on COMPLETED
	} `json:"data"`
}

// ── Transaction (settlement sync) ────────────────────────────────────────────

// getTransactionResponse is returned by GET /transactions/{payment_id}.
// Used by the settlement sync job to check if funds have been settled by Xendit.
// Amounts are floating-point in the provider's currency unit (IDR has 0 decimal places per ISO 4217).
type getTransactionResponse struct {
	ID               string `json:"id"`
	SettlementStatus string `json:"settlement_status"` // "PENDING" | "SETTLED" | "EARLY_SETTLED" | null
	Fee              struct {
		XenditFee                float64 `json:"xendit_fee"`
		ValueAddedTax            float64 `json:"value_added_tax"`
		XenditWithholdingTax     float64 `json:"xendit_withholding_tax"`
		ThirdPartyWithholdingTax float64 `json:"third_party_withholding_tax"`
	} `json:"fee"`
	// EstimatedSettlementTime is the projected date funds arrive in the platform account (ISO 8601).
	EstimatedSettlementTime string `json:"estimated_settlement_time"`
	ReferenceID             string `json:"reference_id"`
	Currency                string `json:"currency"`
}

// ── Account holder & patch ────────────────────────────────────────────────────

// createAccountHolderResponse is returned by POST /account_holders.
type createAccountHolderResponse struct {
	ID string `json:"id"`
}

// patchAccountRequest links an account holder to a sub-account via PATCH /v2/accounts/{id}.
type patchAccountRequest struct {
	AccountHolderID string `json:"account_holder_id"`
}
