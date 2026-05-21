package wilayah

type Area struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

type Meta struct {
	AdministrativeAreaLevel int    `json:"administrative_area_level"`
	UpdatedAt               string `json:"updated_at"`
}

type AreasResponse struct {
	Data []Area `json:"data"`
	Meta Meta   `json:"meta"`
}
