package geocoding

import (
	"context"
)

// Geocoders is an interface for geocoding providers.
type Geocoders interface {
	// GetAddressLatLong returns the latitude and longitude of the given address using the Geoapify API.
	GetAddressLatLong(ctx context.Context, address string) (result *GeocodingResult, err error)

	// ProviderName returns the name of the geocoding provider.
	ProviderName() string
}
