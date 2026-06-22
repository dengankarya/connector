package tokokarya

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type Client struct {
	BaseURL string
	APIKey  string
	http    *http.Client
}

func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		BaseURL: baseURL,
		APIKey:  apiKey,
		http:    &http.Client{Timeout: 15 * time.Second},
	}
}

// ForwardShipmentWebhook forwards a raw Biteship shipment webhook payload to Tokokarya.
func (c *Client) ForwardShipmentWebhook(ctx context.Context, payload []byte) error {
	return c.post(ctx, "/api/webhooks/biteship", payload)
}

// ForwardPaymentWebhook forwards a normalized payment status event to Tokokarya.
func (c *Client) ForwardPaymentWebhook(ctx context.Context, payload []byte) error {
	return c.post(ctx, "/api/webhooks/payment", payload)
}

// CancelExpiredOrders notifies Tokokarya to cancel a specific order via the cron endpoint.
func (c *Client) CancelExpiredOrders(ctx context.Context, orderNumber string) error {
	body, err := json.Marshal(map[string]string{"order_number": orderNumber})
	if err != nil {
		return err
	}
	return c.post(ctx, "/api/cron/cancel-expired-orders", body)
}

func (c *Client) post(ctx context.Context, path string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.APIKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("tokokarya webhook returned unexpected status %d", resp.StatusCode)
	}

	return nil
}
