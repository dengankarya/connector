package shipping

import (
	"context"
	"sync"
	"time"
)

const courierCacheTTL = 30 * 24 * time.Hour

type cachedAggregator struct {
	next      LogisticAggregator
	mu        sync.RWMutex
	couriers  []Courier
	expiresAt time.Time
}

func NewCachedAggregator(next LogisticAggregator) LogisticAggregator {
	return &cachedAggregator{next: next}
}

func (c *cachedAggregator) GetCourierList(ctx context.Context) ([]Courier, error) {
	c.mu.RLock()
	if c.couriers != nil && time.Now().Before(c.expiresAt) {
		couriers := c.couriers
		c.mu.RUnlock()
		return couriers, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	// re-check after acquiring write lock
	if c.couriers != nil && time.Now().Before(c.expiresAt) {
		return c.couriers, nil
	}

	couriers, err := c.next.GetCourierList(ctx)
	if err != nil {
		return nil, err
	}

	c.couriers = couriers
	c.expiresAt = time.Now().Add(courierCacheTTL)
	return c.couriers, nil
}
