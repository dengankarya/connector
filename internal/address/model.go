package address

type AddressSuggestion struct {
	FormattedAddress string  `json:"formattedAddress"`
	Latitude         float64 `json:"latitude"`
	Longitude        float64 `json:"longitude"`
	Country          string  `json:"country"`
	Province         string  `json:"province"`
	City             string  `json:"city"`
	District         string  `json:"district"`
	Village          string  `json:"village"`
	PostalCode       string  `json:"postalCode"`
}

type SearchResult struct {
	Suggestions []AddressSuggestion `json:"suggestions"`
}
