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

type UpdateAccountStatusRequest struct {
	AccountID string `json:"account_id"`
	Status    string `json:"status"`
}

func (c *Client) UpdateAccountStatus(ctx context.Context, accountID, status string) error {
	body, err := json.Marshal(UpdateAccountStatusRequest{
		AccountID: accountID,
		Status:    status,
	})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/webhooks/xenplatform", bytes.NewReader(body))
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
