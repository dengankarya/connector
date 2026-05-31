package biteship

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/dengankarya/connector/internal/shipping/domain"
	shippingProvider "github.com/dengankarya/connector/internal/shipping/provider"
)

type Client struct {
	APIKey  string
	BaseURL string
	http    *http.Client
}

func NewClient(APIKey string, baseURL string) *Client {
	return &Client{
		APIKey:  APIKey,
		BaseURL: baseURL,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) ProviderName() string { return "biteship" }

// GetCourierList fetches couriers from Biteship.
// filteredCouriers is passed as a query param to the API; empty = all.
func (c *Client) GetCourierList(ctx context.Context, filteredCouriers []string) ([]Courier, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/v1/couriers", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", c.APIKey)

	if len(filteredCouriers) > 0 && filteredCouriers[0] != "" {
		q := req.URL.Query()
		for _, code := range filteredCouriers {
			q.Add("couriers", code)
		}
		req.URL.RawQuery = q.Encode()
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		log.WithFields(log.Fields{
			"status": resp.StatusCode,
			"body":   string(raw),
		}).Error("[Biteship] /v1/couriers error response")
		return nil, fmt.Errorf("biteship /v1/couriers returned unexpected status %d", resp.StatusCode)
	}

	var body GetCouriersResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	return body.Couriers, nil
}

// GetRates fetches available courier rates from Biteship's POST /v1/rates/couriers endpoint.
func (c *Client) GetRates(ctx context.Context, req shippingProvider.GetRatesRequest) (*shippingProvider.GetRatesResult, error) {
	body, err := json.Marshal(mapGetRatesRequest(req))
	if err != nil {
		return nil, fmt.Errorf("[Biteship] marshal rates request: %w", err)
	}

	var resp GetShipmentRatesResponse
	if err := c.post(ctx, "/v1/rates/couriers", body, &resp); err != nil {
		return nil, err
	}

	return mapGetRatesResponse(resp), nil
}

// CreateShipment creates a draft order in Biteship and returns the normalized domain shipment.
func (c *Client) CreateShipment(ctx context.Context, req shippingProvider.CreateShipmentRequest) (*domain.Shipment, error) {
	body, err := json.Marshal(mapCreateShipmentRequest(req))
	if err != nil {
		return nil, fmt.Errorf("[Biteship] marshal request: %w", err)
	}

	var resp CreateOrderResponse
	if err := c.post(ctx, "/v1/draft_orders", body, &resp); err != nil {
		return nil, err
	}

	return mapCreateOrderResponse(resp), nil
}

// ConfirmShipment implements provider.ShippingProvider — promotes a Biteship draft order to a live order.
// providerDraftOrderID is the Biteship draft order ID stored in domain.Shipment.ProviderDraftOrderID.
func (c *Client) ConfirmShipment(ctx context.Context, providerDraftOrderID string) (*domain.Shipment, error) {
	var resp CreateOrderResponse
	if err := c.post(ctx, "/v1/draft_orders/"+providerDraftOrderID+"/confirm", nil, &resp); err != nil {
		return nil, err
	}
	return mapConfirmOrderResponse(resp), nil
}

// ─── HTTP helpers ─────────────────────────────────────────────────────────────

func (c *Client) post(ctx context.Context, path string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("[Biteship] build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", c.APIKey)
	return c.do(req, out)
}

func (c *Client) do(req *http.Request, out any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("[Biteship] http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		log.WithFields(log.Fields{
			"method": req.Method,
			"url":    req.URL.String(),
			"status": resp.StatusCode,
			"body":   string(raw),
		}).Error("[Biteship] error response")
		var errBody ErrorResponse
		_ = json.Unmarshal(raw, &errBody)
		return fmt.Errorf("[Biteship] %s %d: %s", req.Method, resp.StatusCode, errBody.Error)
	}

	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("[Biteship] decode response: %w", err)
		}
	}
	return nil
}
