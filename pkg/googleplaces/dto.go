package googleplaces

// Internal Google Places API response types.
// Never referenced outside this package.

type searchResponse struct {
	Places []place `json:"places"`
}

type place struct {
	FormattedAddress  string             `json:"formattedAddress"`
	AddressComponents []addressComponent `json:"addressComponents"`
	Location          location           `json:"location"`
}

type addressComponent struct {
	LongText string   `json:"longText"`
	Types    []string `json:"types"`
}

type location struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}
