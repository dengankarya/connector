package biteship

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/dengankarya/connector/internal/shipping/domain"
	"github.com/dengankarya/connector/internal/shipping/provider"
)

// MapStatus maps a Biteship order status string to the internal domain status.
func MapStatus(status string) domain.ShipmentStatus {
	switch status {
	case "placed", "ready":
		return domain.ShipmentStatusDraft
	case "confirmed", "scheduled":
		return domain.ShipmentStatusWaitingPickup
	case "allocated", "picking_up":
		return domain.ShipmentStatusCourierAssigned
	case "picked":
		return domain.ShipmentStatusPickedUp
	case "in_transit":
		return domain.ShipmentStatusInTransit
	case "dropping_off":
		return domain.ShipmentStatusOutForDelivery
	case "delivered":
		return domain.ShipmentStatusDelivered
	case "return_in_transit":
		return domain.ShipmentStatusReturning
	case "returned":
		return domain.ShipmentStatusReturned
	case "cancelled":
		return domain.ShipmentStatusCancelled
	case "on_hold":
		return domain.ShipmentStatusOnHold
	case "rejected", "disposed", "courier_not_found":
		return domain.ShipmentStatusFailed
	default:
		return domain.ShipmentStatusOnHold
	}
}

// ParseWebhookEventType extracts the event type from a raw Biteship webhook payload.
func ParseWebhookEventType(payload []byte) (string, error) {
	var base struct {
		Event string `json:"event"`
	}
	if err := json.Unmarshal(payload, &base); err != nil {
		return "", fmt.Errorf("parse biteship webhook event: %w", err)
	}
	if base.Event == "" {
		return "", fmt.Errorf("parse biteship webhook event: missing event field")
	}
	return base.Event, nil
}

func mapGetRatesRequest(req provider.GetRatesRequest) GetRatesRequest {
	out := GetRatesRequest{
		OriginAreaID:              req.OriginAreaID,
		DestinationAreaID:         req.DestinationAreaID,
		OriginLatitude:            req.OriginLatitude,
		OriginLongitude:           req.OriginLongitude,
		DestinationLatitude:       req.DestinationLatitude,
		DestinationLongitude:      req.DestinationLongitude,
		OriginPostalCode:          req.OriginPostalCode,
		DestinationPostalCode:     req.DestinationPostalCode,
		Type:                      req.Type,
		Couriers:                  req.Couriers,
		CourierInsurance:          req.CourierInsurance,
		DestinationCashOnDelivery: req.DestinationCashOnDelivery,
		DestinationCODType:        req.DestinationCODType,
		Items:                     make([]CreateOrderItemRequest, 0, len(req.Items)),
	}
	for _, item := range req.Items {
		mapped := CreateOrderItemRequest{
			Name:        item.Name,
			Description: item.Description,
			Category:    item.Category,
			Value:       item.Value,
			Quantity:    item.Quantity,
			Weight:      item.Weight,
			Height:      item.Height,
			Length:      item.Length,
			Width:       item.Width,
		}
		if item.SKU != nil {
			mapped.SKU = *item.SKU
		}
		out.Items = append(out.Items, mapped)
	}
	return out
}

func mapGetRatesResponse(resp GetShipmentRatesResponse) *provider.GetRatesResult {
	result := &provider.GetRatesResult{
		Origin:      mapRatesLocation(resp.Origin),
		Destination: mapRatesLocation(resp.Destination),
		Pricing:     make([]provider.CourierRate, 0, len(resp.Pricing)),
	}
	for _, p := range resp.Pricing {
		result.Pricing = append(result.Pricing, provider.CourierRate{
			AvailableCollectionMethod:    p.AvailableCollectionMethod,
			AvailableForCashOnDelivery:   p.AvailableForCashOnDelivery,
			AvailableForProofOfDelivery:  p.AvailableForProofOfDelivery,
			AvailableForInstantWaybillID: p.AvailableForInstantWaybillID,
			AvailableForInsurance:        p.AvailableForInsurance,
			Company:                      p.Company,
			CourierName:                  p.CourierName,
			CourierCode:                  p.CourierCode,
			CourierServiceName:           p.CourierServiceName,
			CourierServiceCode:           p.CourierServiceCode,
			Currency:                     p.Currency,
			Description:                  p.Description,
			Duration:                     p.Duration,
			ShipmentDurationRange:        p.ShipmentDurationRange,
			ShipmentDurationUnit:         p.ShipmentDurationUnit,
			ServiceType:                  p.ServiceType,
			ShippingType:                 p.ShippingType,
			Price:                        int64(p.Price),
			ShippingFee:                  int64(p.ShippingFee),
			ShippingFeeDiscount:          int64(p.ShippingFeeDiscount),
			ShippingFeeSurcharge:         int64(p.ShippingFeeSurcharge),
			InsuranceFee:                 int64(p.InsuranceFee),
			CashOnDeliveryFee:            int64(p.CashOnDeliveryFee),
		})
	}
	return result
}

func mapRatesLocation(a BiteshipDetailedAddress) provider.RatesLocation {
	loc := provider.RatesLocation{
		PostalCode:      a.PostalCode,
		CountryName:     a.CountryName,
		CountryCode:     a.CountryCode,
		ProvinceName:    a.AdministrativeDivisionLevel1Name,
		CityName:        a.AdministrativeDivisionLevel2Name,
		DistrictName:    a.AdministrativeDivisionLevel3Name,
		SubdistrictName: a.AdministrativeDivisionLevel4Name,
	}
	// Latitude / Longitude come back as interface{} from Biteship.
	if lat, ok := toFloat64(a.Latitude); ok {
		loc.Latitude = &lat
	}
	if lng, ok := toFloat64(a.Longitude); ok {
		loc.Longitude = &lng
	}
	if addr, ok := a.Address.(string); ok {
		loc.Address = addr
	}
	return loc
}

// toFloat64 converts interface{} number values (float64 or json.Number) to float64.
func toFloat64(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	}
	return 0, false
}

func mapCreateShipmentRequest(req provider.CreateShipmentRequest) CreateOrderRequest {
	payload := CreateOrderRequest{
		OriginContactName:  req.Origin.Name,
		OriginContactPhone: req.Origin.Phone,
		OriginAddress:      req.Origin.AddressLine,
		OriginPostalCode:   req.Origin.PostalCode,

		DestinationContactName:  req.Destination.Name,
		DestinationContactPhone: req.Destination.Phone,
		DestinationAddress:      req.Destination.AddressLine,
		DestinationPostalCode:   req.Destination.PostalCode,

		CourierCompany: req.Courier.Company,
		CourierType:    req.Courier.Type,

		DeliveryType: req.Delivery.Type,

		ReferenceID: req.ReferenceID,
		Tags:        req.Tags,
		Metadata:    req.Metadata,

		Items: make([]CreateOrderItemRequest, 0, len(req.Items)),
	}

	if req.Shipper != nil {
		payload.ShipperContactName = req.Shipper.Name
		payload.ShipperContactPhone = req.Shipper.Phone
		if req.Shipper.Email != nil {
			payload.ShipperContactEmail = *req.Shipper.Email
		}
		if req.Shipper.Organization != nil {
			payload.ShipperOrganization = *req.Shipper.Organization
		}
	}

	if req.Origin.Email != nil {
		payload.OriginContactEmail = *req.Origin.Email
	}
	if req.Origin.Note != nil {
		payload.OriginNote = *req.Origin.Note
	}
	if req.Origin.CollectionMethod != nil {
		payload.OriginCollectionMode = *req.Origin.CollectionMethod
	}
	if req.Origin.Latitude != nil && req.Origin.Longitude != nil {
		payload.OriginCoordinate = &CoordinateRequest{
			Latitude:  *req.Origin.Latitude,
			Longitude: *req.Origin.Longitude,
		}
	}

	if req.Destination.Email != nil {
		payload.DestinationContactEmail = *req.Destination.Email
	}
	if req.Destination.Note != nil {
		payload.DestinationNote = *req.Destination.Note
	}
	if req.Destination.Latitude != nil && req.Destination.Longitude != nil {
		payload.DestinationCoordinate = &CoordinateRequest{
			Latitude:  *req.Destination.Latitude,
			Longitude: *req.Destination.Longitude,
		}
	}

	if req.Note != nil {
		payload.OrderNote = *req.Note
	}

	if req.Delivery.UseInsurance && req.Delivery.InsuranceAmount != nil {
		payload.CourierInsurance = req.Delivery.InsuranceAmount
	}
	if req.Delivery.Date != nil {
		payload.DeliveryDate = req.Delivery.Date.Format(time.DateOnly)
		payload.DeliveryTime = req.Delivery.Date.Format("15:04")
	}
	if req.Delivery.COD != nil {
		payload.DestinationCODAmount = &req.Delivery.COD.Amount
		payload.DestinationCODType = req.Delivery.COD.DisbursementType
	}
	if req.Delivery.ProofOfDelivery != nil {
		enabled := true
		payload.DestinationPOD = &enabled
		if req.Delivery.ProofOfDelivery.Note != nil {
			payload.DestinationPODNote = *req.Delivery.ProofOfDelivery.Note
		}
	}

	for _, item := range req.Items {
		mapped := CreateOrderItemRequest{
			Name:        item.Name,
			Description: item.Description,
			Category:    item.Category,
			Value:       item.Value,
			Quantity:    item.Quantity,
			Weight:      item.Weight,
			Height:      item.Height,
			Length:      item.Length,
			Width:       item.Width,
		}
		if item.SKU != nil {
			mapped.SKU = *item.SKU
		}
		payload.Items = append(payload.Items, mapped)
	}

	return payload
}

// mapConfirmOrderResponse maps a Biteship confirm draft order response to a domain.Shipment.
// In the confirm response, "id" is the live order ID and "draft_order_id" is the original draft ID.
func mapConfirmOrderResponse(resp CreateOrderResponse) *domain.Shipment {
	shipment := &domain.Shipment{
		Provider:           "biteship",
		ProviderOrderID:    &resp.ID,
		CourierCode:        resp.Courier.Company,
		CourierServiceCode: resp.Courier.Type,
		ShippingCost:       resp.Price,
		Status:             MapStatus(resp.Status),
	}
	if resp.DraftOrderID != nil {
		shipment.ProviderDraftOrderID = resp.DraftOrderID
	}
	if resp.Courier.WaybillID != nil {
		shipment.TrackingNumber = *resp.Courier.WaybillID
	}
	if resp.Courier.Link != nil {
		shipment.TrackingURL = *resp.Courier.Link
	}
	if resp.ConfirmedAt != nil {
		t := resp.ConfirmedAt.Time
		shipment.ConfirmedAt = &t
	} else if resp.ReadyAt != nil {
		t := resp.ReadyAt.Time
		shipment.ConfirmedAt = &t
	}
	return shipment
}

func mapCreateOrderResponse(resp CreateOrderResponse) *domain.Shipment {
	shipment := &domain.Shipment{
		Provider:             "biteship",
		ProviderDraftOrderID: &resp.ID,
		ProviderOrderID:      resp.OrderID,
		CourierCode:          resp.Courier.Company,
		CourierServiceCode:   resp.Courier.Type,
		ShippingCost:         resp.Price,
		Status:               MapStatus(resp.Status),
		CreatedAt:            resp.CreatedAt.Time,
	}

	if resp.Courier.WaybillID != nil {
		shipment.TrackingNumber = *resp.Courier.WaybillID
	}
	if resp.Courier.Link != nil {
		shipment.TrackingURL = *resp.Courier.Link
	}
	if resp.ReadyAt != nil {
		t := resp.ReadyAt.Time
		shipment.ConfirmedAt = &t
	}
	if resp.ConfirmedAt != nil {
		t := resp.ConfirmedAt.Time
		shipment.ConfirmedAt = &t
	}
	if resp.Status == "picked" && resp.UpdatedAt.Time.After(resp.CreatedAt.Time) {
		t := resp.UpdatedAt.Time
		shipment.PickedUpAt = &t
	}
	if resp.Status == "delivered" && resp.UpdatedAt.Time.After(resp.CreatedAt.Time) {
		t := resp.UpdatedAt.Time
		shipment.DeliveredAt = &t
	}

	return shipment
}
