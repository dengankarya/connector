package biteship

import (
	"bytes"
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

func (c *Client) GetRates(ctx context.Context, req shipping.RateRequest) ([]shipping.Rate, error) {
	url := fmt.Sprintf("%s/v1/rates/couriers", c.BaseURL)
	
	items := make([]RateItem, len(req.Items))
	for i, it := range req.Items {
		items[i] = RateItem{
			Name:        it.Name,
			Description: it.Description,
			Value:       it.Value,
			Length:      it.Length,
			Width:       it.Width,
			Height:      it.Height,
			Weight:      it.Weight,
			Quantity:    it.Quantity,
		}
	}

	biteshipReq := GetRatesRequest{
		OriginPostalCode:      req.OriginPostalCode,
		DestinationPostalCode: req.DestinationPostalCode,
		Couriers:              req.Couriers,
		Items:                 items,
	}

	reqBody, err := json.Marshal(biteshipReq)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", c.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("biteship /v1/rates/couriers returned unexpected status %d", resp.StatusCode)
	}

	var body GetRatesResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}

	if !body.Success {
		return nil, fmt.Errorf("biteship rates api returned error: %s", body.Message)
	}

	rates := make([]shipping.Rate, len(body.Pricing))
	for i, r := range body.Pricing {
		rates[i] = shipping.Rate{
			CourierName:        r.CourierName,
			CourierCode:        r.CourierCode,
			CourierServiceName: r.CourierServiceName,
			CourierServiceCode: r.CourierServiceCode,
			Duration:           r.Duration,
			Price:              r.Price,
			Type:               r.Type,
		}
	}
	return rates, nil
}
