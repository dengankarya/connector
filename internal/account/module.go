package account

import (
	"github.com/dengankarya/connector/pkg/logger"
	"github.com/dengankarya/connector/pkg/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Module contains all account-related services.
type Module struct {
	Service    *Service
	Repository *Repository // exposed for webhook handler construction in main
}

// NewModule constructs the account service.
func NewModule(
	pool *pgxpool.Pool,
	txRunner *postgres.TxRunner,
	logger logger.Logger,
) *Module {
	if pool == nil {
		return &Module{
			Service: NewService(nil, nil, logger),
		}
	}

	repo := NewRepository(pool)
	service := NewService(repo, txRunner, logger)

	return &Module{
		Service:    service,
		Repository: repo,
	}
}
