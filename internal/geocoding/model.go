package geocoding

type GeocodingResult struct {
	Query  string  `json:"query"`
	Places []Place `json:"places"`
}

type Place struct {
	Name      string  `json:"name"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}
