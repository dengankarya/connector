package geoapify

import "github.com/dengankarya/connector/internal/geocoding"

func mapGeocodingResponseFeature(feature Feature) geocoding.Place {
	return geocoding.Place{
		Name:      feature.Properties.Formatted,
		Latitude:  feature.Properties.Lat,
		Longitude: feature.Properties.Lon,
	}
}

func mapGeocodingResponse(response GeocodingResponse) geocoding.GeocodingResult {
	places := make([]geocoding.Place, len(response.Features))
	for i, feature := range response.Features {
		places[i] = mapGeocodingResponseFeature(feature)
	}

	return geocoding.GeocodingResult{
		Query:  response.Query.Text,
		Places: places,
	}
}
