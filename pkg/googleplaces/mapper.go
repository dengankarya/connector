package googleplaces

import "github.com/dengankarya/connector/internal/address"

func mapSuggestions(places []place) []address.AddressSuggestion {
	out := make([]address.AddressSuggestion, 0, len(places))
	for _, p := range places {
		out = append(out, mapSuggestion(p))
	}
	return out
}

func mapSuggestion(p place) address.AddressSuggestion {
	s := address.AddressSuggestion{
		FormattedAddress: p.FormattedAddress,
		Latitude:         p.Location.Latitude,
		Longitude:        p.Location.Longitude,
	}
	for _, c := range p.AddressComponents {
		for _, t := range c.Types {
			switch t {
			case "country":
				s.Country = c.LongText
			case "administrative_area_level_1":
				s.Province = c.LongText
			case "administrative_area_level_2":
				s.City = c.LongText
			case "administrative_area_level_3":
				s.District = c.LongText
			case "administrative_area_level_4":
				s.Village = c.LongText
			case "postal_code":
				s.PostalCode = c.LongText
			}
		}
	}
	return s
}
