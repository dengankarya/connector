package shipping

import (
	"github.com/dengankarya/connector/internal/shipping/domain"
	"github.com/dengankarya/connector/internal/shipping/provider"
	shipmentrepo "github.com/dengankarya/connector/internal/shipping/repository"
	"github.com/dengankarya/connector/pkg/logger"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Module contains all shipping-related services.
type Module struct {
	Service    *ShippingService
	Repository *shipmentrepo.ShipmentRepository
}

// NewModule constructs the shipping service.
// logisticsClient and accountManager can be nil when not configured.
func NewModule(
	pool *pgxpool.Pool,
	logisticsClient LogisticAggregator,
	shippingProvider provider.ShippingProvider,
	accountManager domain.AccountManager,
	logger *logger.Logger,
) *Module {
	var shipmentRepo *shipmentrepo.ShipmentRepository
	if pool != nil {
		shipmentRepo = shipmentrepo.NewShipmentRepository(pool)
	}

	cachedAggregator := NewCachedAggregator(logisticsClient)
	service := NewShippingService(cachedAggregator, shippingProvider, shipmentRepo, accountManager)

	return &Module{
		Service:    service,
		Repository: shipmentRepo,
	}
}
