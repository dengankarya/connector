package xenplatform

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/dengankarya/overwatch/internal/payment"
)

type Client struct {
	APIKey  string
	BaseURL string
	http    *http.Client
}

func NewClient(apiKey, baseURL string) *Client {
	return &Client{
		APIKey:  apiKey,
		BaseURL: baseURL,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) CreateAccount(ctx context.Context, req payment.CreateAccountRequest) (*payment.Account, error) {
	body, err := json.Marshal(createAccountRequestDTO{
		Email: req.Email,
		Type:  req.Type,
		PublicProfile: publicProfileDTO{
			BusinessName: req.PublicProfile.BusinessName,
		},
	})
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v2/accounts", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.SetBasicAuth(c.APIKey, "")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("xendit POST /v2/accounts returned unexpected status %d", resp.StatusCode)
	}

	var dto accountDTO
	if err := json.NewDecoder(resp.Body).Decode(&dto); err != nil {
		return nil, err
	}

	return toAccount(dto), nil
}

func (c *Client) GetAccount(ctx context.Context, id string) (*payment.Account, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/v2/accounts/"+id, nil)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Accept", "application/json")
	httpReq.SetBasicAuth(c.APIKey, "")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("xendit GET /v2/accounts/%s returned unexpected status %d", id, resp.StatusCode)
	}

	var dto accountDTO
	if err := json.NewDecoder(resp.Body).Decode(&dto); err != nil {
		return nil, err
	}

	return toAccount(dto), nil
}

func toAccount(dto accountDTO) *payment.Account {
	return &payment.Account{
		ID:    dto.ID,
		Email: dto.Email,
		Type:  dto.Type,
		PublicProfile: payment.PublicProfile{
			BusinessName: dto.PublicProfile.BusinessName,
		},
		Status:  dto.Status,
		Country: dto.Country,
		Created: dto.Created,
		Updated: dto.Updated,
	}
}
