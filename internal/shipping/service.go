package shipping

import "context"

type LogisticAggregator interface {
	GetCourierList(ctx context.Context) ([]Courier, error)
	GetRates(ctx context.Context, req RateRequest) ([]Rate, error)
}

type ShippingService struct {
	repo LogisticAggregator
}

func NewShippingService(repo LogisticAggregator) *ShippingService {
	return &ShippingService{repo: repo}
}

func (s *ShippingService) GetCourierList(ctx context.Context) ([]Courier, error) {
	return s.repo.GetCourierList(ctx)
}

func (s *ShippingService) GetRates(ctx context.Context, req RateRequest) ([]Rate, error) {
	return s.repo.GetRates(ctx, req)
}
