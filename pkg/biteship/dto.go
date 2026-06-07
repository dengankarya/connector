package biteship

import (
	"fmt"
	"strings"
	"time"
)

// BiteshipTime is a time.Time wrapper that handles Biteship's non-standard timestamp formats.
// Biteship sometimes omits seconds: "2006-01-02T15:04+07:00" instead of the RFC3339 standard
// "2006-01-02T15:04:05Z07:00".
type BiteshipTime struct {
	time.Time
}

var biteshipTimeFormats = []string{
	time.RFC3339,             // with seconds:    "2006-01-02T15:04:05Z07:00"
	"2006-01-02T15:04Z07:00", // without seconds: "2006-01-02T15:04+07:00"
}

func (t *BiteshipTime) UnmarshalJSON(data []byte) error {
	s := strings.Trim(string(data), `"`)
	if s == "null" || s == "" {
		return nil
	}
	for _, format := range biteshipTimeFormats {
		if parsed, err := time.Parse(format, s); err == nil {
			t.Time = parsed
			return nil
		}
	}
	return fmt.Errorf("cannot parse %q as Biteship timestamp", s)
}

// ─── Courier list ─────────────────────────────────────────────────────────────

type GetCouriersResponse struct {
	Success  bool      `json:"success"`
	Object   string    `json:"object"`
	Couriers []Courier `json:"couriers"`
}

type Courier struct {
	AvailableCollectionMethod    []string `json:"available_collection_method"`
	AvailableForCashOnDelivery   bool     `json:"available_for_cash_on_delivery"`
	AvailableForProofOfDelivery  bool     `json:"available_for_proof_of_delivery"`
	AvailableForInstantWaybillID bool     `json:"available_for_instant_waybill_id"`
	CourierName                  string   `json:"courier_name"`
	CourierCode                  string   `json:"courier_code"`
	CourierServiceName           string   `json:"courier_service_name"`
	CourierServiceCode           string   `json:"courier_service_code"`
	Tier                         string   `json:"tier,omitempty"`
	Description                  string   `json:"description"`
	ServiceType                  string   `json:"service_type"`
	ShippingType                 string   `json:"shipping_type"`
	ShipmentDurationRange        string   `json:"shipment_duration_range"`
	ShipmentDurationUnit         string   `json:"shipment_duration_unit"`
}

// ─── Error ────────────────────────────────────────────────────────────────────

type ErrorResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
}

// ─── Create order request ─────────────────────────────────────────────────────

type CreateOrderRequest struct {
	ShipperContactName  string `json:"shipper_contact_name,omitempty"`
	ShipperContactPhone string `json:"shipper_contact_phone,omitempty"`
	ShipperContactEmail string `json:"shipper_contact_email,omitempty"`
	ShipperOrganization string `json:"shipper_organization,omitempty"`

	OriginContactName    string             `json:"origin_contact_name"`
	OriginContactPhone   string             `json:"origin_contact_phone"`
	OriginContactEmail   string             `json:"origin_contact_email,omitempty"`
	OriginAddress        string             `json:"origin_address"`
	OriginNote           string             `json:"origin_note,omitempty"`
	OriginPostalCode     string             `json:"origin_postal_code,omitempty"`
	OriginCoordinate     *CoordinateRequest `json:"origin_coordinate,omitempty"`
	OriginCollectionMode string             `json:"origin_collection_method,omitempty"`

	DestinationContactName  string             `json:"destination_contact_name"`
	DestinationContactPhone string             `json:"destination_contact_phone"`
	DestinationContactEmail string             `json:"destination_contact_email,omitempty"`
	DestinationAddress      string             `json:"destination_address"`
	DestinationNote         string             `json:"destination_note,omitempty"`
	DestinationPostalCode   string             `json:"destination_postal_code,omitempty"`
	DestinationCoordinate   *CoordinateRequest `json:"destination_coordinate,omitempty"`
	DestinationCODAmount    *int64             `json:"destination_cash_on_delivery,omitempty"`
	DestinationCODType      string             `json:"destination_cash_on_delivery_type,omitempty"`
	DestinationPOD          *bool              `json:"destination_proof_of_delivery,omitempty"`
	DestinationPODNote      string             `json:"destination_proof_of_delivery_note,omitempty"`

	CourierCompany   string `json:"courier_company,omitempty"`
	CourierType      string `json:"courier_type,omitempty"`
	CourierInsurance *int64 `json:"courier_insurance,omitempty"`

	DeliveryType string `json:"delivery_type"`
	DeliveryDate string `json:"delivery_date,omitempty"`
	DeliveryTime string `json:"delivery_time,omitempty"`

	OrderNote string `json:"order_note,omitempty"`

	Metadata    map[string]any `json:"metadata,omitempty"`
	ReferenceID string         `json:"reference_id,omitempty"`
	Tags        []string       `json:"tags,omitempty"`

	Items []CreateOrderItemRequest `json:"items"`
}

type CoordinateRequest struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

type CreateOrderItemRequest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Category    string `json:"category,omitempty"`
	SKU         string `json:"sku,omitempty"`

	Value    int64 `json:"value"`
	Quantity int   `json:"quantity"`
	Weight   int64 `json:"weight"`

	Height int64 `json:"height,omitempty"`
	Length int64 `json:"length,omitempty"`
	Width  int64 `json:"width,omitempty"`
}

// ─── Create order response ────────────────────────────────────────────────────

type CreateOrderResponse struct {
	Success bool   `json:"success"`
	Code    int64  `json:"code"`
	Object  string `json:"object"`

	ID           string  `json:"id"`
	OrderID      *string `json:"order_id"`
	DraftOrderID *string `json:"draft_order_id"`

	Origin      OrderAddressResponse `json:"origin"`
	Destination OrderAddressResponse `json:"destination"`

	Courier  CourierResponse  `json:"courier"`
	Delivery DeliveryResponse `json:"delivery"`

	Extra    []any               `json:"extra"`
	Tags     []string            `json:"tags"`
	Metadata map[string]any      `json:"metadata"`
	Items    []OrderItemResponse `json:"items"`

	Currency string `json:"currency"`
	TaxLines []any  `json:"tax_lines"`
	Price    int64  `json:"price"`
	Status   string `json:"status"`

	ReferenceID *string `json:"reference_id"`
	InvoiceID   *string `json:"invoice_id"`
	UserID      *string `json:"user_id"`

	CreatedAt   BiteshipTime  `json:"created_at"`
	UpdatedAt   BiteshipTime  `json:"updated_at"`
	PlacedAt    *BiteshipTime `json:"placed_at"`
	ReadyAt     *BiteshipTime `json:"ready_at"`
	ConfirmedAt *BiteshipTime `json:"confirmed_at"`
	DeletedAt   *BiteshipTime `json:"deleted_at"`
}

type OrderAddressResponse struct {
	AreaID string `json:"area_id"`

	Address string  `json:"address"`
	Note    *string `json:"note"`

	ContactName  string  `json:"contact_name"`
	ContactPhone string  `json:"contact_phone"`
	ContactEmail *string `json:"contact_email"`

	Coordinate CoordinateResponse `json:"coordinate"`

	ProvinceName string `json:"province_name"`
	CityName     string `json:"city_name"`
	DistrictName string `json:"district_name"`

	PostalCode int64 `json:"postal_code"`

	CollectionMethod *string `json:"collection_method"`

	ProofOfDelivery *ProofOfDeliveryResponse `json:"proof_of_delivery"`
	CashOnDelivery  *CashOnDeliveryResponse  `json:"cash_on_delivery"`
}

type CoordinateResponse struct {
	Latitude  *float64 `json:"latitude"`
	Longitude *float64 `json:"longitude"`
}

type ProofOfDeliveryResponse struct {
	Use  bool    `json:"use"`
	Fee  int64   `json:"fee"`
	Note *string `json:"note"`
	Link *string `json:"link"`
}

type CashOnDeliveryResponse struct {
	PaymentMethod  *string `json:"payment_method"`
	Amount         *int64  `json:"amount"`
	AmountCurrency string  `json:"amount_currency"`
	Note           *string `json:"note"`
	Type           *string `json:"type"`
}

type CourierResponse struct {
	Name  *string `json:"name"`
	Phone *string `json:"phone"`

	Company string `json:"company"`
	Type    string `json:"type"`

	Link *string `json:"link"`

	TrackingID *string `json:"tracking_id"`
	WaybillID  *string `json:"waybill_id"`

	Insurance   CourierInsuranceResponse `json:"insurance"`
	RoutingCode *string                  `json:"routing_code"`
}

type CourierInsuranceResponse struct {
	Amount         int64  `json:"amount"`
	Fee            int64  `json:"fee"`
	Note           string `json:"note"`
	AmountCurrency string `json:"amount_currency"`
	FeeCurrency    string `json:"fee_currency"`
}

type DeliveryResponse struct {
	Type         string        `json:"type"`
	Datetime     *BiteshipTime `json:"datetime"`
	Note         *string       `json:"note"`
	Distance     *float64      `json:"distance"`
	DistanceUnit string        `json:"distance_unit"`
}

type OrderItemResponse struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Value       int64  `json:"value"`
	Currency    string `json:"currency"`
	Quantity    int    `json:"quantity"`
	Height      int64  `json:"height"`
	Width       int64  `json:"width"`
	Length      int64  `json:"length"`
	Weight      int64  `json:"weight"`
}

// ─── Courier rates request ────────────────────────────────────────────────────

// GetRatesRequest is the request body sent to Biteship's POST /v1/rates/couriers endpoint.
type GetRatesRequest struct {
	OriginAreaID      string `json:"origin_area_id,omitempty"`
	DestinationAreaID string `json:"destination_area_id,omitempty"`

	OriginLatitude       *float64 `json:"origin_latitude,omitempty"`
	OriginLongitude      *float64 `json:"origin_longitude,omitempty"`
	DestinationLatitude  *float64 `json:"destination_latitude,omitempty"`
	DestinationLongitude *float64 `json:"destination_longitude,omitempty"`

	OriginPostalCode      *int `json:"origin_postal_code,omitempty"`
	DestinationPostalCode *int `json:"destination_postal_code,omitempty"`

	Type     string `json:"type,omitempty"`
	Couriers string `json:"couriers"`

	Items []CreateOrderItemRequest `json:"items"`

	CourierInsurance          *int64 `json:"courier_insurance,omitempty"`
	DestinationCashOnDelivery *int64 `json:"destination_cash_on_delivery,omitempty"`
	DestinationCODType        string `json:"destination_cash_on_delivery_type,omitempty"`
}

// ─── Webhook events ───────────────────────────────────────────────────────────

const (
	WebhookEventOrderStatus    = "order.status"
	WebhookEventOrderPrice     = "order.price"
	WebhookEventOrderWaybillID = "order.waybill_id"
)

// OrderStatusEvent is the payload for the order.status webhook.
type OrderStatusEvent struct {
	Event                    string `json:"event"`
	CourierTrackingID        string `json:"courier_tracking_id"`
	CourierWaybillID         string `json:"courier_waybill_id"`
	CourierCompany           string `json:"courier_company"`
	CourierType              string `json:"courier_type"`
	CourierDriverName        string `json:"courier_driver_name"`
	CourierDriverPhone       string `json:"courier_driver_phone"`
	CourierDriverPhotoURL    string `json:"courier_driver_photo_url"`
	CourierDriverPlateNumber string `json:"courier_driver_plate_number"`
	CourierLink              string `json:"courier_link"`
	OrderID                  string `json:"order_id"`
	OrderPrice               int64  `json:"order_price"`
	Status                   string `json:"status"`
}

// OrderPriceEvent is the payload for the order.price webhook.
type OrderPriceEvent struct {
	CashOnDeliveryFee  int64  `json:"cash_on_delivery_fee"`
	CourierTrackingID  string `json:"courier_tracking_id"`
	CourierWaybillID   string `json:"courier_waybill_id"`
	Event              string `json:"event"`
	OrderID            string `json:"order_id"`
	Price              int64  `json:"price"`
	ProofOfDeliveryFee int64  `json:"proof_of_delivery_fee"`
	ShipmentFee        int64  `json:"shippment_fee"`
	Status             string `json:"status"`
}

// OrderWaybillIDEvent is the payload for the order.waybill_id webhook.
type OrderWaybillIDEvent struct {
	OrderID           string `json:"order_id"`
	CourierTrackingID string `json:"courier_tracking_id"`
	CourierWaybillID  string `json:"courier_waybill_id"`
	Event             string `json:"event"`
	Status            string `json:"status"`
}

// GetShipmentRatesResponse is the response from Biteship's Get Shipment Rates API,
// which is used to fetch available shipping options and their rates for a given shipment.
type GetShipmentRatesResponse struct {
	Success     bool                      `json:"success"`
	Object      string                    `json:"object"`
	Message     string                    `json:"message"`
	Code        int                       `json:"code"`
	Origin      BiteshipDetailedAddress   `json:"origin"`
	Destination BiteshipDetailedAddress   `json:"destination"`
	Stops       []interface{}             `json:"stops"`
	Pricing     []BiteshipPricingResponse `json:"pricing"`
}

type BiteshipDetailedAddress struct {
	LocationID                       interface{} `json:"location_id"`
	Latitude                         interface{} `json:"latitude"`
	Longitude                        interface{} `json:"longitude"`
	PostalCode                       int         `json:"postal_code"`
	CountryName                      string      `json:"country_name"`
	CountryCode                      string      `json:"country_code"`
	AdministrativeDivisionLevel1Name string      `json:"administrative_division_level_1_name"`
	AdministrativeDivisionLevel1Type string      `json:"administrative_division_level_1_type"`
	AdministrativeDivisionLevel2Name string      `json:"administrative_division_level_2_name"`
	AdministrativeDivisionLevel2Type string      `json:"administrative_division_level_2_type"`
	AdministrativeDivisionLevel3Name string      `json:"administrative_division_level_3_name"`
	AdministrativeDivisionLevel3Type string      `json:"administrative_division_level_3_type"`
	AdministrativeDivisionLevel4Name string      `json:"administrative_division_level_4_name"`
	AdministrativeDivisionLevel4Type string      `json:"administrative_division_level_4_type"`
	Address                          interface{} `json:"address"`
}

type BiteshipPricingResponse struct {
	AvailableCollectionMethod    []string      `json:"available_collection_method"`
	AvailableForCashOnDelivery   bool          `json:"available_for_cash_on_delivery"`
	AvailableForProofOfDelivery  bool          `json:"available_for_proof_of_delivery"`
	AvailableForInstantWaybillID bool          `json:"available_for_instant_waybill_id"`
	AvailableForInsurance        bool          `json:"available_for_insurance"`
	Company                      string        `json:"company"`
	CourierName                  string        `json:"courier_name"`
	CourierCode                  string        `json:"courier_code"`
	CourierServiceName           string        `json:"courier_service_name"`
	CourierServiceCode           string        `json:"courier_service_code"`
	Currency                     string        `json:"currency"`
	Description                  string        `json:"description"`
	Duration                     string        `json:"duration"`
	ShipmentDurationRange        string        `json:"shipment_duration_range"`
	ShipmentDurationUnit         string        `json:"shipment_duration_unit"`
	ServiceType                  string        `json:"service_type"`
	ShippingType                 string        `json:"shipping_type"`
	Price                        int           `json:"price"`
	ShippingFee                  int           `json:"shipping_fee"`
	ShippingFeeDiscount          int           `json:"shipping_fee_discount"`
	ShippingFeeSurcharge         int           `json:"shipping_fee_surcharge"`
	InsuranceFee                 int           `json:"insurance_fee"`
	CashOnDeliveryFee            int           `json:"cash_on_delivery_fee"`
	TaxLines                     []interface{} `json:"tax_lines"`
	Type                         string        `json:"type"`
}
