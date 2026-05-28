package wilayah

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/dengankarya/connector/internal/region"
)

type Client struct {
	baseURL string
	http    *http.Client
}

func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) GetProvinces(ctx context.Context) ([]region.Area, error) {
	return c.fetch(ctx, fmt.Sprintf("%s/api/provinces.json", c.baseURL))
}

func (c *Client) GetRegencies(ctx context.Context, provinceCode string) ([]region.Area, error) {
	return c.fetch(ctx, fmt.Sprintf("%s/api/regencies/%s.json", c.baseURL, provinceCode))
}

func (c *Client) GetDistricts(ctx context.Context, regencyCode string) ([]region.Area, error) {
	return c.fetch(ctx, fmt.Sprintf("%s/api/districts/%s.json", c.baseURL, regencyCode))
}

func (c *Client) GetVillages(ctx context.Context, districtCode string) ([]region.Area, error) {
	return c.fetch(ctx, fmt.Sprintf("%s/api/villages/%s.json", c.baseURL, districtCode))
}

func (c *Client) fetch(ctx context.Context, url string) ([]region.Area, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("wilayah.id returned unexpected status %d", resp.StatusCode)
	}

	var body AreasResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}

	areas := make([]region.Area, len(body.Data))
	for i, a := range body.Data {
		areas[i] = region.Area{Code: a.Code, Name: a.Name}
	}
	return areas, nil
}
