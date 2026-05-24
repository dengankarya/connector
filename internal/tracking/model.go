package tracking

type PublicTracking struct {
	ID          string            `json:"id"`
	WaybillID   string            `json:"waybill_id"`
	Courier     CourierInfo       `json:"courier"`
	Origin      LocationInfo      `json:"origin"`
	Destination LocationInfo      `json:"destination"`
	History     []TrackingHistory `json:"history"`
	Link        string            `json:"link,omitempty"`
	OrderID     string            `json:"order_id,omitempty"`
	Status      string            `json:"status"`
}

type CourierInfo struct {
	Company           string `json:"company"`
	Name              string `json:"name,omitempty"`
	Phone             string `json:"phone,omitempty"`
	DriverName        string `json:"driver_name,omitempty"`
	DriverPhone       string `json:"driver_phone,omitempty"`
	DriverPhotoURL    string `json:"driver_photo_url,omitempty"`
	DriverPlateNumber string `json:"driver_plate_number,omitempty"`
}

type LocationInfo struct {
	ContactName string `json:"contact_name,omitempty"`
	Address     string `json:"address,omitempty"`
}

type TrackingHistory struct {
	Note      string `json:"note"`
	UpdatedAt string `json:"updated_at"`
	Status    string `json:"status"`
}
