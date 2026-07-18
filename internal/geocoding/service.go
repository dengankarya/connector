package geocoding

import (
	"context"

	"github.com/dengankarya/connector/pkg/logger"
	"github.com/sirupsen/logrus"
)

type Service struct {
	geocoders Geocoders
	logger    logger.Logger
}

func NewService(geocoders Geocoders, logger logger.Logger) *Service {
	return &Service{geocoders: geocoders, logger: logger}
}

// Geocode returns the latitude and longitude of the given address using the geocoding provider.
func (s *Service) Geocode(ctx context.Context, address string) (*GeocodingResult, error) {
	result, err := s.geocoders.GetAddressLatLong(ctx, address)
	if err != nil {
		s.logger.WithFields(ctx, logrus.Fields{
			"address": address,
			"err":     err,
		}).Warn("geocode failed")

		return nil, err
	}

	return result, nil
}
