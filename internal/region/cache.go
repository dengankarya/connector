package region

import (
	"context"
	"fmt"
	"sync"
)

type cachedClient struct {
	next  RegionClient
	mu    sync.RWMutex
	store map[string][]Area
}

func NewCachedClient(next RegionClient) RegionClient {
	return &cachedClient{
		next:  next,
		store: make(map[string][]Area),
	}
}

func (c *cachedClient) GetProvinces(ctx context.Context) ([]Area, error) {
	return c.get(ctx, "provinces", func() ([]Area, error) {
		return c.next.GetProvinces(ctx)
	})
}

func (c *cachedClient) GetRegencies(ctx context.Context, provinceCode string) ([]Area, error) {
	return c.get(ctx, fmt.Sprintf("regencies:%s", provinceCode), func() ([]Area, error) {
		return c.next.GetRegencies(ctx, provinceCode)
	})
}

func (c *cachedClient) GetDistricts(ctx context.Context, regencyCode string) ([]Area, error) {
	return c.get(ctx, fmt.Sprintf("districts:%s", regencyCode), func() ([]Area, error) {
		return c.next.GetDistricts(ctx, regencyCode)
	})
}

func (c *cachedClient) GetVillages(ctx context.Context, districtCode string) ([]Area, error) {
	return c.get(ctx, fmt.Sprintf("villages:%s", districtCode), func() ([]Area, error) {
		return c.next.GetVillages(ctx, districtCode)
	})
}

func (c *cachedClient) get(_ context.Context, key string, fetch func() ([]Area, error)) ([]Area, error) {
	c.mu.RLock()
	if data, ok := c.store[key]; ok {
		c.mu.RUnlock()
		return data, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	if data, ok := c.store[key]; ok {
		return data, nil
	}

	data, err := fetch()
	if err != nil {
		return nil, err
	}

	c.store[key] = data
	return data, nil
}
