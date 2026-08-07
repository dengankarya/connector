package address

import "context"

// AddressSearcher is the swappable provider interface for address search.
// Implement this to swap Google Places for HERE, Mapbox, or any other provider.
type AddressSearcher interface {
	Search(ctx context.Context, query string) ([]AddressSuggestion, error)
	ProviderName() string
}
