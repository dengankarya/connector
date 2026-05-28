package shipping

import (
	"context"
	"sync"
	"time"

	"github.com/dengankarya/connector/pkg/biteship"
)

const courierCacheTTL = 30 * 24 * time.Hour

type cachedAggregator struct {
	next      LogisticAggregator
	mu        sync.RWMutex
	couriers  []biteship.Courier
	expiresAt time.Time
}

func NewCachedAggregator(next LogisticAggregator) LogisticAggregator {
	return &cachedAggregator{next: next}
}

// GetCourierList returns couriers, using a long-lived cache.
// The full list is always fetched and cached; filtering is applied in-memory
// so different filter combinations share one cache entry.
func (c *cachedAggregator) GetCourierList(ctx context.Context, couriers []string) ([]biteship.Courier, error) {
	c.mu.RLock()
	if c.couriers != nil && time.Now().Before(c.expiresAt) {
		result := filterCouriers(c.couriers, couriers)
		c.mu.RUnlock()
		return result, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	// Re-check after acquiring write lock.
	if c.couriers != nil && time.Now().Before(c.expiresAt) {
		return filterCouriers(c.couriers, couriers), nil
	}

	// Fetch all couriers (no filter at the API level).
	all, err := c.next.GetCourierList(ctx, nil)
	if err != nil {
		return nil, err
	}

	c.couriers = all
	c.expiresAt = time.Now().Add(courierCacheTTL)
	return filterCouriers(c.couriers, couriers), nil
}

// filterCouriers returns couriers whose CourierCode is in codes.
// Returns all couriers when codes is empty.
func filterCouriers(all []biteship.Courier, codes []string) []biteship.Courier {
	if len(codes) == 0 || (len(codes) == 1 && codes[0] == "") {
		return all
	}
	set := make(map[string]struct{}, len(codes))
	for _, code := range codes {
		set[code] = struct{}{}
	}
	result := make([]biteship.Courier, 0, len(codes))
	for _, co := range all {
		if _, ok := set[co.CourierCode]; ok {
			result = append(result, co)
		}
	}
	return result
}
