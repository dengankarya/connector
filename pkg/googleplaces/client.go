package googleplaces

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/dengankarya/connector/internal/address"
)

const (
	placesBaseURL = "https://places.googleapis.com/v1"
	fieldMask     = "places.formattedAddress,places.addressComponents,places.location"
)

type Client struct {
	apiKey string
	http   *http.Client
}

func NewClient(apiKey string) *Client {
	return &Client{
		apiKey: apiKey,
		http:   &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) ProviderName() string { return "google_places" }

func (c *Client) Search(ctx context.Context, query string) ([]address.AddressSuggestion, error) {
	body, err := json.Marshal(map[string]string{"textQuery": query})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, placesBaseURL+"/places:searchText", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Goog-Api-Key", c.apiKey)
	req.Header.Set("X-Goog-FieldMask", fieldMask)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("google places: %d: %s", resp.StatusCode, data)
	}

	var result searchResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("google places: unmarshal: %w", err)
	}

	return mapSuggestions(result.Places), nil
}
