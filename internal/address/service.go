package address

import (
	"context"

	"github.com/sirupsen/logrus"
)

type Service struct {
	searcher AddressSearcher
	logger   *logrus.Logger
}

func NewService(searcher AddressSearcher, logger *logrus.Logger) *Service {
	return &Service{searcher: searcher, logger: logger}
}

func (s *Service) Search(ctx context.Context, query string) (*SearchResult, error) {
	suggestions, err := s.searcher.Search(ctx, query)
	if err != nil {
		s.logger.WithFields(logrus.Fields{"query": query, "err": err}).Warn("address search failed")
		return nil, err
	}
	return &SearchResult{Suggestions: suggestions}, nil
}
