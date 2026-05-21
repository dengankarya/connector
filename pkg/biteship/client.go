package biteship

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/dengankarya/overwatch/internal/shipping"
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
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *Client) GetCourierList(ctx context.Context) ([]shipping.Courier, error) {
	url := fmt.Sprintf("%s/v1/couriers", c.BaseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", c.APIKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("biteship /v1/couriers returned unexpected status %d", resp.StatusCode)
	}

	var body GetCouriersResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}

	couriers := make([]shipping.Courier, len(body.Couriers))
	for i, c := range body.Couriers {
		couriers[i] = shipping.Courier{
			AvailableCollectionMethod:    c.AvailableCollectionMethod,
			AvailableForCashOnDelivery:   c.AvailableForCashOnDelivery,
			AvailableForProofOfDelivery:  c.AvailableForProofOfDelivery,
			AvailableForInstantWaybillID: c.AvailableForInstantWaybillID,
			CourierName:                  c.CourierName,
			CourierCode:                  c.CourierCode,
			CourierServiceName:           c.CourierServiceName,
			CourierServiceCode:           c.CourierServiceCode,
			Tier:                         c.Tier,
			Description:                  c.Description,
			ServiceType:                  c.ServiceType,
			ShippingType:                 c.ShippingType,
			ShipmentDurationRange:        c.ShipmentDurationRange,
			ShipmentDurationUnit:         c.ShipmentDurationUnit,
		}
	}
	return couriers, nil
}
