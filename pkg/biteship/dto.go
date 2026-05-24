package biteship

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

type GetTrackingResponse struct {
	Success     bool              `json:"success"`
	Message     string            `json:"message"`
	Object      string            `json:"object"`
	ID          string            `json:"id"`
	WaybillID   string            `json:"waybill_id"`
	Courier     TrackingCourier   `json:"courier"`
	Origin      TrackingLocation  `json:"origin"`
	Destination TrackingLocation  `json:"destination"`
	History     []TrackingHistory `json:"history"`
	Link        string            `json:"link,omitempty"`
	OrderID     string            `json:"order_id,omitempty"`
	Status      string            `json:"status"`
}

type TrackingCourier struct {
	Company           string `json:"company"`
	Name              string `json:"name,omitempty"`
	Phone             string `json:"phone,omitempty"`
	DriverName        string `json:"driver_name,omitempty"`
	DriverPhone       string `json:"driver_phone,omitempty"`
	DriverPhotoURL    string `json:"driver_photo_url,omitempty"`
	DriverPlateNumber string `json:"driver_plate_number,omitempty"`
}

type TrackingLocation struct {
	ContactName string `json:"contact_name,omitempty"`
	Address     string `json:"address,omitempty"`
}

type TrackingHistory struct {
	Note      string `json:"note"`
	UpdatedAt string `json:"updated_at"`
	Status    string `json:"status"`
}

type GetRatesRequest struct {
	OriginPostalCode      int        `json:"origin_postal_code,omitempty"`
	DestinationPostalCode int        `json:"destination_postal_code,omitempty"`
	Couriers              string     `json:"couriers"`
	Items                 []RateItem `json:"items"`
}

type RateItem struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Value       int    `json:"value"`
	Length      int    `json:"length,omitempty"`
	Width       int    `json:"width,omitempty"`
	Height      int    `json:"height,omitempty"`
	Weight      int    `json:"weight"`
	Quantity    int    `json:"quantity"`
}

type GetRatesResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Pricing []Rate `json:"pricing"`
}

type Rate struct {
	CourierName        string `json:"courier_name"`
	CourierCode        string `json:"courier_code"`
	CourierServiceName string `json:"courier_service_name"`
	CourierServiceCode string `json:"courier_service_code"`
	Duration           string `json:"duration"`
	Price              int    `json:"price"`
	Type               string `json:"type"`
}
