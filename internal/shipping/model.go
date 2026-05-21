package shipping

type Courier struct {
	AvailableCollectionMethod    []string
	AvailableForCashOnDelivery   bool
	AvailableForProofOfDelivery  bool
	AvailableForInstantWaybillID bool
	CourierName                  string
	CourierCode                  string
	CourierServiceName           string
	CourierServiceCode           string
	Tier                         string
	Description                  string
	ServiceType                  string
	ShippingType                 string
	ShipmentDurationRange        string
	ShipmentDurationUnit         string
}
