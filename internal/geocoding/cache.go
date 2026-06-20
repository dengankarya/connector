package geocoding

import (
	"context"
	"sync"
)

type cachedGeocoder struct {
	next  Geocoders
	mu    sync.RWMutex
	store map[string]*GeocodingResult
}

func NewCachedGeocoder(next Geocoders) Geocoders {
	return &cachedGeocoder{
		next:  next,
		store: make(map[string]*GeocodingResult),
	}
}

func (c *cachedGeocoder) ProviderName() string { return c.next.ProviderName() }

func (c *cachedGeocoder) GetAddressLatLong(ctx context.Context, address string) (*GeocodingResult, error) {
	c.mu.RLock()
	if r, ok := c.store[address]; ok {
		c.mu.RUnlock()
		return r, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	if r, ok := c.store[address]; ok {
		return r, nil
	}

	r, err := c.next.GetAddressLatLong(ctx, address)
	if err != nil {
		return nil, err
	}

	c.store[address] = r
	return r, nil
}
