package geoapify

type GeocodingResponse struct {
	Features []Feature `json:"features"`
	Query    Query     `json:"query"`
}
type Feature struct {
	Properties Property `json:"properties"`
}

type Property struct {
	Lat       float64 `json:"lat"`
	Lon       float64 `json:"lon"`
	Distance  int     `json:"distance"`
	Formatted string  `json:"formatted"`
}

type Query struct {
	Text string `json:"text"`
}
