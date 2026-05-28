package tokokarya

import (
	"bytes"
	"context"
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

// ForwardWebhook forwards a raw Xendit payment webhook payload to Tokokarya.
// The payload is sent as-is (no re-marshalling) so Tokokarya receives exactly
// what Xendit sent.
func (c *Client) ForwardWebhook(ctx context.Context, payload []byte) error {
	return c.post(ctx, "/api/webhooks/xenplatform", payload)
}

// ForwardShipmentWebhook forwards a raw Biteship shipment webhook payload to Tokokarya.
func (c *Client) ForwardShipmentWebhook(ctx context.Context, payload []byte) error {
	return c.post(ctx, "/api/webhooks/biteship", payload)
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
