package provider

import (
	"context"
	"time"

	"github.com/dengankarya/connector/internal/shipping/domain"
)

// CreateShipmentRequest represents a normalized shipment creation request
// shared across all shipping provider implementations.
//
// This request intentionally remains provider-agnostic and should only
// contain business/domain shipment information.
type CreateShipmentRequest struct {
	// ReferenceID is the internal order or shipment identifier.
	//
	// Providers may use this value as an idempotency key or external reference.
	ReferenceID string `json:"reference_id"`

	// Shipper contains optional shipper or merchant information.
	Shipper *Shipper `json:"shipper,omitempty"`

	// Origin contains pickup or sender address information.
	Origin Address `json:"origin"`

	// Destination contains receiver or delivery address information.
	Destination Address `json:"destination"`

	// Items contains the list of shipment items.
	Items []ShipmentItem `json:"items"`

	// Courier contains the selected courier service.
	Courier CourierSelection `json:"courier"`

	// Delivery contains shipment delivery configuration.
	Delivery DeliveryOptions `json:"delivery"`

	// Tags contains optional provider-level tagging information.
	Tags []string `json:"tags,omitempty"`

	// Note contains additional shipment notes.
	Note *string `json:"note,omitempty"`

	// Metadata contains arbitrary internal metadata.
	//
	// This field may be forwarded to providers that support metadata storage.
	Metadata map[string]any `json:"metadata,omitempty"`
}

// Shipper represents the shipment owner or merchant information.
type Shipper struct {
	// Name is the shipper contact name.
	Name string `json:"name"`

	// Phone is the shipper contact phone number.
	Phone string `json:"phone"`

	// Email is the optional shipper email address.
	Email *string `json:"email,omitempty"`

	// Organization is the optional organization or company name.
	Organization *string `json:"organization,omitempty"`
}

// Address represents a shipment address.
//
// Coordinates are optional and may be required by providers
// that support instant or location-based delivery routing.
type Address struct {
	// Name is the contact person's name.
	Name string `json:"name"`

	// Phone is the contact phone number.
	Phone string `json:"phone"`

	// Email is the optional contact email address.
	Email *string `json:"email,omitempty"`

	// AddressLine is the complete delivery or pickup address.
	AddressLine string `json:"address_line"`

	// Note contains additional delivery or pickup instructions.
	Note *string `json:"note,omitempty"`

	// PostalCode is the postal or ZIP code.
	PostalCode string `json:"postal_code"`

	// Latitude is the optional coordinate latitude.
	Latitude *float64 `json:"latitude,omitempty"`

	// Longitude is the optional coordinate longitude.
	Longitude *float64 `json:"longitude,omitempty"`

	// CollectionMethod defines how the shipment will be handed over.
	//
	// Example values:
	//   - pickup
	//   - drop_off
	CollectionMethod *string `json:"collection_method,omitempty"`
}

// ShipmentItem represents an item included in a shipment.
type ShipmentItem struct {
	// Name is the item name.
	Name string `json:"name"`

	// Description is the optional item description.
	Description string `json:"description,omitempty"`

	// Category represents the shipment item category.
	//
	// Example values:
	//   - fashion
	//   - electronic
	//   - groceries
	//   - frozen_food
	//   - others
	Category string `json:"category,omitempty"`

	// SKU is the optional stock keeping unit identifier.
	SKU *string `json:"sku,omitempty"`

	// Quantity is the total item quantity.
	Quantity int `json:"quantity"`

	// Weight is the item weight in grams.
	Weight int64 `json:"weight"`

	// Length is the item length in centimeters.
	Length int64 `json:"length,omitempty"`

	// Width is the item width in centimeters.
	Width int64 `json:"width,omitempty"`

	// Height is the item height in centimeters.
	Height int64 `json:"height,omitempty"`

	// Value is the declared item value
	// in the smallest currency unit.
	Value int64 `json:"value"`
}

// CourierSelection represents the selected courier service.
type CourierSelection struct {
	// Company is the courier provider code.
	//
	// Example values:
	//   - jne
	//   - sicepat
	//   - anteraja
	Company string `json:"company"`

	// Type is the courier service type.
	//
	// Example values:
	//   - reg
	//   - yes
	//   - instant
	Type string `json:"type"`
}

// DeliveryOptions contains shipment delivery configuration.
type DeliveryOptions struct {
	// Type is the shipment delivery type.
	//
	// Example values:
	//   - now
	//   - scheduled
	Type string `json:"type"`

	// Date is the optional scheduled delivery date and time.
	//
	// This field is primarily used for scheduled deliveries.
	Date *time.Time `json:"date,omitempty"`

	// UseInsurance determines whether shipment insurance is enabled.
	UseInsurance bool `json:"use_insurance,omitempty"`

	// InsuranceAmount is the declared insurance value
	// in the smallest currency unit.
	InsuranceAmount *int64 `json:"insurance_amount,omitempty"`

	// COD contains optional cash-on-delivery configuration.
	COD *CODOptions `json:"cod,omitempty"`

	// ProofOfDelivery contains optional proof-of-delivery configuration.
	ProofOfDelivery *ProofOfDeliveryOptions `json:"proof_of_delivery,omitempty"`
}

// CODOptions contains cash-on-delivery configuration.
type CODOptions struct {
	// Amount is the cash-on-delivery amount
	// in the smallest currency unit.
	Amount int64 `json:"amount"`

	// DisbursementType defines the COD settlement window.
	//
	// Example values:
	//   - 3_days
	//   - 5_days
	//   - 7_days
	DisbursementType string `json:"disbursement_type"`
}

// ProofOfDeliveryOptions contains proof-of-delivery configuration.
type ProofOfDeliveryOptions struct {
	// Note contains additional instructions
	// for proof-of-delivery handling.
	Note *string `json:"note,omitempty"`
}

// GetRatesRequest is the normalized input for fetching courier rates.
// Exactly one of (OriginAreaID+DestinationAreaID), coordinate pair, or
// postal code pair must be supplied, optionally mixed (e.g. postal origin +
// coordinate destination).
type GetRatesRequest struct {
	// Area ID — highest accuracy; cannot be used with instant couriers.
	OriginAreaID      string `json:"origin_area_id,omitempty"`
	DestinationAreaID string `json:"destination_area_id,omitempty"`

	// Coordinates — required for instant couriers (Gojek, Grab, etc.).
	OriginLatitude       *float64 `json:"origin_latitude,omitempty"`
	OriginLongitude      *float64 `json:"origin_longitude,omitempty"`
	DestinationLatitude  *float64 `json:"destination_latitude,omitempty"`
	DestinationLongitude *float64 `json:"destination_longitude,omitempty"`

	// Postal codes — medium accuracy, easiest to implement.
	OriginPostalCode      *int `json:"origin_postal_code,omitempty"`
	DestinationPostalCode *int `json:"destination_postal_code,omitempty"`

	// Type enables special rate modes (e.g. "origin_suggestion_to_closest_destination").
	Type string `json:"type,omitempty"`

	// Couriers is a comma-separated list of courier codes (required).
	Couriers string `json:"couriers"`

	// Items is the list of items to ship (required).
	Items []ShipmentItem `json:"items"`

	// CourierInsurance is the declared item value for insurance (optional).
	CourierInsurance *int64 `json:"courier_insurance,omitempty"`

	// DestinationCashOnDelivery activates COD; cannot exceed IDR 15,000,000 (optional).
	DestinationCashOnDelivery *int64 `json:"destination_cash_on_delivery,omitempty"`

	// DestinationCODType is the COD disbursement window: "3_days", "5_days", or "7_days".
	DestinationCODType string `json:"destination_cash_on_delivery_type,omitempty"`
}

// RatesLocation holds the resolved address details for origin or destination
// returned by the provider.
type RatesLocation struct {
	Latitude    *float64 `json:"latitude,omitempty"`
	Longitude   *float64 `json:"longitude,omitempty"`
	PostalCode  int      `json:"postal_code,omitempty"`
	CountryName string   `json:"country_name,omitempty"`
	CountryCode string   `json:"country_code,omitempty"`

	ProvinceName    string `json:"province_name,omitempty"`
	CityName        string `json:"city_name,omitempty"`
	DistrictName    string `json:"district_name,omitempty"`
	SubdistrictName string `json:"subdistrict_name,omitempty"`

	Address string `json:"address,omitempty"`
}

// CourierRate is a single pricing option returned by the provider.
type CourierRate struct {
	AvailableCollectionMethod    []string `json:"available_collection_method"`
	AvailableForCashOnDelivery   bool     `json:"available_for_cash_on_delivery"`
	AvailableForProofOfDelivery  bool     `json:"available_for_proof_of_delivery"`
	AvailableForInstantWaybillID bool     `json:"available_for_instant_waybill_id"`
	AvailableForInsurance        bool     `json:"available_for_insurance"`

	Company            string `json:"company"`
	CourierName        string `json:"courier_name"`
	CourierCode        string `json:"courier_code"`
	CourierServiceName string `json:"courier_service_name"`
	CourierServiceCode string `json:"courier_service_code"`

	Currency    string `json:"currency"`
	Description string `json:"description"`
	Duration    string `json:"duration"`

	ShipmentDurationRange string `json:"shipment_duration_range"`
	ShipmentDurationUnit  string `json:"shipment_duration_unit"`
	ServiceType           string `json:"service_type"`
	ShippingType          string `json:"shipping_type"`

	// Price is the final amount after any discounts/surcharges and optional fees.
	Price int64 `json:"price"`
	// ShippingFee is the base shipping cost before any adjustments.
	ShippingFee          int64 `json:"shipping_fee"`
	ShippingFeeDiscount  int64 `json:"shipping_fee_discount"`
	ShippingFeeSurcharge int64 `json:"shipping_fee_surcharge"`
	InsuranceFee         int64 `json:"insurance_fee"`
	CashOnDeliveryFee    int64 `json:"cash_on_delivery_fee"`
}

// GetRatesResult is the normalized response from the provider's rate API.
type GetRatesResult struct {
	Origin      RatesLocation `json:"origin"`
	Destination RatesLocation `json:"destination"`
	Pricing     []CourierRate `json:"pricing"`
}

// ShippingProvider defines a shipping provider implementation.
//
// Implementations are responsible for translating normalized
// shipment requests into provider-specific API requests and responses.
type ShippingProvider interface {
	// CreateShipment creates a shipment/order
	// in the external shipping provider.
	//
	// The returned shipment must be normalized into the internal
	// shipment domain model regardless of the provider implementation.
	CreateShipment(
		ctx context.Context,
		req CreateShipmentRequest,
	) (*domain.Shipment, error)

	// GetRates returns available courier rates for the given shipment parameters.
	// Used by the frontend before creating a draft order so the merchant can
	// select a courier service and see the pricing.
	GetRates(ctx context.Context, req GetRatesRequest) (*GetRatesResult, error)

	// ProviderName returns the canonical provider identifier.
	//
	// Example values:
	//   - biteship
	//   - jne
	//   - gosend
	ProviderName() string
}
