package biteship

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/dengankarya/overwatch/internal/shipping"
	"github.com/dengankarya/overwatch/internal/tracking"
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

func (c *Client) GetPublicTracking(ctx context.Context, waybillID string, courierCode string) (tracking.PublicTracking, error) {
	url := fmt.Sprintf("%s/v1/trackings/%s/couriers/%s", c.BaseURL, waybillID, courierCode)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return tracking.PublicTracking{}, err
	}
	req.Header.Set("Authorization", c.APIKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return tracking.PublicTracking{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return tracking.PublicTracking{}, fmt.Errorf("biteship /v1/trackings returned unexpected status %d", resp.StatusCode)
	}

	var body GetTrackingResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return tracking.PublicTracking{}, err
	}
	
	if !body.Success {
		return tracking.PublicTracking{}, fmt.Errorf("biteship tracking api returned error: %s", body.Message)
	}

	histories := make([]tracking.TrackingHistory, len(body.History))
	for i, h := range body.History {
		histories[i] = tracking.TrackingHistory{
			Note:      h.Note,
			UpdatedAt: h.UpdatedAt,
			Status:    h.Status,
		}
	}

	return tracking.PublicTracking{
		ID:        body.ID,
		WaybillID: body.WaybillID,
		Courier: tracking.CourierInfo{
			Company:           body.Courier.Company,
			Name:              body.Courier.Name,
			Phone:             body.Courier.Phone,
			DriverName:        body.Courier.DriverName,
			DriverPhone:       body.Courier.DriverPhone,
			DriverPhotoURL:    body.Courier.DriverPhotoURL,
			DriverPlateNumber: body.Courier.DriverPlateNumber,
		},
		Origin: tracking.LocationInfo{
			ContactName: body.Origin.ContactName,
			Address:     body.Origin.Address,
		},
		Destination: tracking.LocationInfo{
			ContactName: body.Destination.ContactName,
			Address:     body.Destination.Address,
		},
		History: histories,
		Link:    body.Link,
		OrderID: body.OrderID,
		Status:  body.Status,
	}, nil
}
