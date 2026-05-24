package tracking

import (
	"context"

	"github.com/dengankarya/overwatch/internal/shipping"
)

type LogisticTracker interface {
	GetPublicTracking(ctx context.Context, waybillID string, courierCode string) (PublicTracking, error)
	GetCourierList(ctx context.Context) ([]shipping.Courier, error)
}

type TrackingService struct {
	repo LogisticTracker
}

func NewTrackingService(repo LogisticTracker) *TrackingService {
	return &TrackingService{repo: repo}
}

func (s *TrackingService) GetPublicTracking(ctx context.Context, waybillID string, courierCode string) (PublicTracking, error) {
	return s.repo.GetPublicTracking(ctx, waybillID, courierCode)
}

func (s *TrackingService) GetCourierList(ctx context.Context) ([]shipping.Courier, error) {
	return s.repo.GetCourierList(ctx)
}
