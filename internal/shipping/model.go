package shipping

type Courier struct {
	AvailableCollectionMethod    []string `json:"available_collection_method,omitempty"`
	AvailableForCashOnDelivery   bool     `json:"available_for_cash_on_delivery,omitempty"`
	AvailableForProofOfDelivery  bool     `json:"available_for_proof_of_delivery,omitempty"`
	AvailableForInstantWaybillID bool     `json:"available_for_instant_waybill_id,omitempty"`
	CourierName                  string   `json:"courier_name,omitempty"`
	CourierCode                  string   `json:"courier_code,omitempty"`
	CourierServiceName           string   `json:"courier_service_name,omitempty"`
	CourierServiceCode           string   `json:"courier_service_code,omitempty"`
	Tier                         string   `json:"tier,omitempty"`
	Description                  string   `json:"description,omitempty"`
	ServiceType                  string   `json:"service_type,omitempty"`
	ShippingType                 string   `json:"shipping_type,omitempty"`
	ShipmentDurationRange        string   `json:"shipment_duration_range,omitempty"`
	ShipmentDurationUnit         string   `json:"shipment_duration_unit,omitempty"`
}
