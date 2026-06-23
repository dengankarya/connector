package doku

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
)

// Client is a thin HTTP client for the Doku Sub-Account (SAC) API.
type Client struct {
	clientID  string
	secretKey string
	baseURL   string
	http      *http.Client
}

// NewClient creates a Doku API client.
// baseURL: "https://api.doku.com" (production) or "https://api-sandbox.doku.com" (sandbox).
func NewClient(clientID, secretKey, baseURL string) *Client {
	return &Client{
		clientID:  clientID,
		secretKey: secretKey,
		baseURL:   baseURL,
		http:      &http.Client{Timeout: 30 * time.Second},
	}
}

// CreateSubAccount calls POST /sac-merchant/v1/accounts.
// Returns the gateway-assigned account ID (e.g. "SAC-0000-0000000000001") and initial status.
func (c *Client) CreateSubAccount(ctx context.Context, email, name string) (gatewayAccountID, status string, err error) {
	type reqBody struct {
		Account struct {
			Email string `json:"email"`
			Type  string `json:"type"`
			Name  string `json:"name"`
		} `json:"account"`
	}
	type respBody struct {
		Account struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"account"`
	}

	req := reqBody{}
	req.Account.Email = email
	req.Account.Type = "STANDARD"
	req.Account.Name = name

	var resp respBody
	if err := c.post(ctx, "/sac-merchant/v1/accounts", req, &resp); err != nil {
		return "", "", fmt.Errorf("doku: create sub-account: %w", err)
	}
	return resp.Account.ID, resp.Account.Status, nil
}

// GetBalance calls GET /sac-merchant/v1/balances/{account_id}.
// Returns pending and available amounts in the smallest currency unit (IDR).
func (c *Client) GetBalance(ctx context.Context, gatewayAccountID string) (pending, available int64, err error) {
	type respBody struct {
		Balance struct {
			Pending   string `json:"pending"`
			Available string `json:"available"`
		} `json:"balance"`
	}

	var resp respBody
	if err := c.get(ctx, "/sac-merchant/v1/balances/"+gatewayAccountID, &resp); err != nil {
		return 0, 0, fmt.Errorf("doku: get balance: %w", err)
	}

	p, _ := strconv.ParseInt(resp.Balance.Pending, 10, 64)
	a, _ := strconv.ParseInt(resp.Balance.Available, 10, 64)
	return p, a, nil
}

// SendPayout calls POST /sac-merchant/v1/payouts.
// Returns the payout status reported by Doku (e.g. "COMPLETED").
func (c *Client) SendPayout(
	ctx context.Context,
	gatewayAccountID string,
	amount int64,
	invoiceNumber, bankCode, bankAccountNumber, bankAccountName string,
) (status string, err error) {
	type reqBody struct {
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
		Payout struct {
			Amount        int64  `json:"amount"`
			InvoiceNumber string `json:"invoice_number"`
		} `json:"payout"`
		Beneficiary struct {
			BankCode          string `json:"bank_code"`
			BankAccountNumber string `json:"bank_account_number"`
			BankAccountName   string `json:"bank_account_name"`
		} `json:"beneficiary"`
	}
	type respBody struct {
		Payout struct {
			Status string `json:"status"`
		} `json:"payout"`
	}

	req := reqBody{}
	req.Account.ID = gatewayAccountID
	req.Payout.Amount = amount
	req.Payout.InvoiceNumber = invoiceNumber
	req.Beneficiary.BankCode = bankCode
	req.Beneficiary.BankAccountNumber = bankAccountNumber
	req.Beneficiary.BankAccountName = bankAccountName

	var resp respBody
	if err := c.post(ctx, "/sac-merchant/v1/payouts", req, &resp); err != nil {
		return "", fmt.Errorf("doku: send payout: %w", err)
	}
	return resp.Payout.Status, nil
}

// CheckoutResult is the response from CreateCheckout.
type CheckoutResult struct {
	SessionID   string // DOKU session_id — use as ProviderInvoiceID
	PaymentURL  string // customer-facing checkout page URL
	ExpiredDate string // raw "yyyyMMddHHmmss" string in UTC+7 (WIB)
}

// CreateCheckout calls POST /checkout/v1/payment and returns the checkout session.
// dueMins controls how long the checkout link stays valid (0 lets DOKU use its default of 60 min).
// accountID is the SAC sub-account ID (e.g. "SAC-0000-..."); pass empty string when not applicable.
func (c *Client) CreateCheckout(ctx context.Context, invoiceNumber string, amount int64, dueMins int, customerName, customerEmail, customerPhone, accountID string) (*CheckoutResult, error) {
	type reqBody struct {
		Order struct {
			Amount        int64  `json:"amount"`
			InvoiceNumber string `json:"invoice_number"`
		} `json:"order"`
		Payment struct {
			PaymentDueDate int `json:"payment_due_date,omitempty"`
		} `json:"payment"`
		Customer struct {
			Name  string `json:"name,omitempty"`
			Email string `json:"email,omitempty"`
			Phone string `json:"phone,omitempty"`
		} `json:"customer"`
		AdditionalInfo *struct {
			Account struct {
				ID string `json:"id"`
			} `json:"account"`
		} `json:"additional_info,omitempty"`
	}
	type respBody struct {
		Response struct {
			Payment struct {
				URL         string `json:"url"`
				ExpiredDate string `json:"expired_date"`
			} `json:"payment"`
			Order struct {
				SessionID string `json:"session_id"`
			} `json:"order"`
		} `json:"response"`
	}

	var req reqBody
	req.Order.Amount = amount
	req.Order.InvoiceNumber = invoiceNumber
	req.Payment.PaymentDueDate = dueMins
	req.Customer.Name = customerName
	req.Customer.Email = customerEmail
	req.Customer.Phone = customerPhone
	if accountID != "" {
		req.AdditionalInfo = &struct {
			Account struct {
				ID string `json:"id"`
			} `json:"account"`
		}{}
		req.AdditionalInfo.Account.ID = accountID
	}

	var resp respBody
	if err := c.post(ctx, "/checkout/v1/payment", req, &resp); err != nil {
		return nil, fmt.Errorf("doku: create checkout: %w", err)
	}
	return &CheckoutResult{
		SessionID:   resp.Response.Order.SessionID,
		PaymentURL:  resp.Response.Payment.URL,
		ExpiredDate: resp.Response.Payment.ExpiredDate,
	}, nil
}

// ── HTTP helpers ──────────────────────────────────────────────────────────────

func (c *Client) post(ctx context.Context, path string, body any, out any) error {
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	reqID := uuid.New().String()
	ts := time.Now().UTC().Format(time.RFC3339)
	sig := SignRequest(c.clientID, reqID, ts, path, c.secretKey, bodyBytes)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Client-Id", c.clientID)
	req.Header.Set("Request-Id", reqID)
	req.Header.Set("Request-Timestamp", ts)
	req.Header.Set("Signature", sig)

	return c.do(req, out)
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	reqID := uuid.New().String()
	ts := time.Now().UTC().Format(time.RFC3339)
	sig := SignRequest(c.clientID, reqID, ts, path, c.secretKey, nil)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Client-Id", c.clientID)
	req.Header.Set("Request-Id", reqID)
	req.Header.Set("Request-Timestamp", ts)
	req.Header.Set("Signature", sig)

	return c.do(req, out)
}

func (c *Client) do(req *http.Request, out any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("status %d: %s", resp.StatusCode, respBytes)
	}
	if out != nil {
		if err := json.Unmarshal(respBytes, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
