package geoapify

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/dengankarya/connector/internal/geocoding"
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

func (c *Client) ProviderName() string { return "geoapify" }

// GetAddressLatLong returns the latitude and longitude of the given address using the Geoapify API.
func (c *Client) GetAddressLatLong(ctx context.Context, address string) (result *geocoding.GeocodingResult, err error) {
	url := fmt.Sprintf(
		"/v1/geocode/search?apiKey=%s&text=%s",
		c.APIKey,
		url.QueryEscape(address),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return &geocoding.GeocodingResult{
			Query: address,
		}, nil
	}

	var responseBody GeocodingResponse
	if err := json.NewDecoder(resp.Body).Decode(&responseBody); err != nil {
		return nil, err
	}

	queryResult := mapGeocodingResponse(responseBody)
	return &queryResult, nil
}
