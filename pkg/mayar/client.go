package mayar

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client is a thin HTTP client for the Mayar payment API.
type Client struct {
	apiKey  string
	baseURL string
	http    *http.Client
}

// NewClient creates a Mayar API client.
// baseURL defaults to "https://api.mayar.id" in production; use "https://api.mayar.club" for sandbox.
func NewClient(apiKey, baseURL string) *Client {
	return &Client{
		apiKey:  apiKey,
		baseURL: baseURL,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// ── Invoice creation ──────────────────────────────────────────────────────────

// CreateInvoiceRequest is the request body for POST /hl/v1/invoice/create.
type CreateInvoiceRequest struct {
	Name        string    `json:"name"`
	Email       string    `json:"email"`
	Mobile      string    `json:"mobile"`
	RedirectURL string    `json:"redirectUrl"`
	Description string    `json:"description"`
	ExpiredAt   string    `json:"expiredAt"` // ISO 8601 UTC, e.g. "2024-01-01T00:00:00Z"
	Items       []Item    `json:"items"`
	ExtraData   ExtraData `json:"extraData"`
}

// Item is a single line item on a Mayar invoice.
type Item struct {
	Quantity    int    `json:"quantity"`
	Rate        int64  `json:"rate"`
	Description string `json:"description"`
}

// ExtraData holds custom metadata forwarded to Mayar with the invoice.
type ExtraData struct {
	NoCustomer string `json:"noCustomer"` // caller's idempotency / customer reference
	IDProd     string `json:"idProd"`     // caller's product / order identifier
}

// CreateInvoiceResponse is the response body from POST /hl/v1/invoice/create.
type CreateInvoiceResponse struct {
	StatusCode int    `json:"statusCode"`
	Messages   string `json:"messages"`
	Data       struct {
		ID            string    `json:"id"`
		TransactionID string    `json:"transactionId"`
		Link          string    `json:"link"`
		ExpiredAt     int64     `json:"expiredAt"` // Unix milliseconds
		ExtraData     ExtraData `json:"extraData"`
	} `json:"data"`
}

// ── Transaction listing ───────────────────────────────────────────────────────

// TransactionFee is a single fee line item on a settled transaction.
type TransactionFee struct {
	ID                 string `json:"id"`
	BalanceHistoryType string `json:"balanceHistoryType"` // "mayar_fee" | "xendit_fee"
	Debit              int64  `json:"debit"`
}

// Transaction represents a single entry in the Mayar balance history.
type Transaction struct {
	ID                       string           `json:"id"`
	Credit                   int64            `json:"credit"` // gross amount received
	Status                   string           `json:"status"` // "paid" | "settled"
	PaymentMethod            string           `json:"paymentMethod"`
	CreatedAt                int64            `json:"createdAt"`                // Unix milliseconds
	PaymentLinkTransactionID string           `json:"paymentLinkTransactionId"` // our provider_invoice_id
	Fee                      []TransactionFee `json:"fee"`
}

// ListTransactionsResponse is the response from GET /hl/v1/transactions.
type ListTransactionsResponse struct {
	StatusCode int           `json:"statusCode"`
	Messages   string        `json:"messages"`
	HasMore    bool          `json:"hasMore"`
	PageCount  int           `json:"pageCount"`
	PageSize   int           `json:"pageSize"`
	Page       int           `json:"page"`
	Data       []Transaction `json:"data"`
}

// ListTransactionsRequest is the input for GET /hl/v1/transactions.
type ListTransactionsRequest struct {
	Page     int    // 1-based page number
	PageSize int    // default 10
	Status   string // "paid" | "settled" | "" for all
	StartAt  int64  // Unix milliseconds (inclusive)
	EndAt    int64  // Unix milliseconds (inclusive); 0 = no upper bound
}

// ListTransactions fetches paginated transactions from GET /hl/v1/transactions.
func (c *Client) ListTransactions(ctx context.Context, req ListTransactionsRequest) (*ListTransactionsResponse, error) {
	page := req.Page
	if page < 1 {
		page = 1
	}
	pageSize := req.PageSize
	if pageSize <= 0 {
		pageSize = 50
	}

	u := fmt.Sprintf("%s/hl/v1/transactions?page=%d&pageSize=%d", c.baseURL, page, pageSize)
	if req.Status != "" {
		u += "&status=" + req.Status
	}
	if req.StartAt > 0 {
		u += fmt.Sprintf("&startAt=%d", req.StartAt)
	}
	if req.EndAt > 0 {
		u += fmt.Sprintf("&endAt=%d", req.EndAt)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("mayar: build list transactions request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("mayar: list transactions http: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("mayar: read list transactions response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("mayar: list transactions status %d: %s", resp.StatusCode, respBody)
	}

	var out ListTransactionsResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("mayar: decode list transactions response: %w", err)
	}

	return &out, nil
}

// ── Invoice creation ───────────────────────────────────────────────────────────

// CreateInvoice calls POST /hl/v1/invoice/create and returns the invoice details.
func (c *Client) CreateInvoice(ctx context.Context, req CreateInvoiceRequest) (*CreateInvoiceResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("mayar: marshal create invoice request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/hl/v1/invoice/create", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("mayar: build create invoice request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("mayar: create invoice http: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("mayar: read create invoice response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("mayar: create invoice status %d: %s", resp.StatusCode, respBody)
	}

	var out CreateInvoiceResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("mayar: decode create invoice response: %w", err)
	}

	return &out, nil
}
