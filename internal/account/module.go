package account

import (
	"github.com/dengankarya/connector/pkg/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sirupsen/logrus"
)

// Module contains all account-related services.
type Module struct {
	Service *Service
}

// NewModule constructs the account service.
// gatewayClient can be nil when Doku is not configured.
func NewModule(
	pool *pgxpool.Pool,
	txRunner *postgres.TxRunner,
	gatewayClient GatewayClient,
	logger *logrus.Logger,
) *Module {
	if pool == nil {
		return &Module{
			Service: NewService(nil, nil, nil, logger),
		}
	}

	repo := NewRepository(pool)
	service := NewService(repo, txRunner, gatewayClient, logger)

	return &Module{
		Service: service,
	}
}
