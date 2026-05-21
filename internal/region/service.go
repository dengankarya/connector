package region

import "context"

type RegionClient interface {
	GetProvinces(ctx context.Context) ([]Area, error)
	GetRegencies(ctx context.Context, provinceCode string) ([]Area, error)
	GetDistricts(ctx context.Context, regencyCode string) ([]Area, error)
	GetVillages(ctx context.Context, districtCode string) ([]Area, error)
}

type RegionService struct {
	client RegionClient
}

func NewRegionService(client RegionClient) *RegionService {
	return &RegionService{client: client}
}

func (s *RegionService) GetProvinces(ctx context.Context) ([]Area, error) {
	return s.client.GetProvinces(ctx)
}

func (s *RegionService) GetRegencies(ctx context.Context, provinceCode string) ([]Area, error) {
	return s.client.GetRegencies(ctx, provinceCode)
}

func (s *RegionService) GetDistricts(ctx context.Context, regencyCode string) ([]Area, error) {
	return s.client.GetDistricts(ctx, regencyCode)
}

func (s *RegionService) GetVillages(ctx context.Context, districtCode string) ([]Area, error) {
	return s.client.GetVillages(ctx, districtCode)
}
