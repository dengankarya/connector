package account

import (
	"github.com/dengankarya/connector/pkg/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sirupsen/logrus"
)

// Module contains all account-related services.
type Module struct {
	Service      *Service
	Repository   *Repository         // exposed for webhook handler construction in main
	XenditClient XenditGatewayClient // exposed so RegisterHandlers can serve the GetAccount route
}

// NewModule constructs the account service.
// dokuClient and xenditClient may be nil when the respective gateway is not configured.
func NewModule(
	pool *pgxpool.Pool,
	txRunner *postgres.TxRunner,
	dokuClient GatewayClient,
	xenditClient XenditGatewayClient,
	logger *logrus.Logger,
) *Module {
	if pool == nil {
		return &Module{
			Service:      NewService(nil, nil, nil, nil, logger),
			XenditClient: xenditClient,
		}
	}

	repo := NewRepository(pool)
	service := NewService(repo, txRunner, dokuClient, xenditClient, logger)

	return &Module{
		Service:      service,
		Repository:   repo,
		XenditClient: xenditClient,
	}
}
