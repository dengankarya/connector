package admin

import (
	"github.com/dengankarya/connector/internal/account"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sirupsen/logrus"
)

// Module wires together the admin service and exposes route registration helpers.
type Module struct {
	Service *Service
}

// NewModule constructs the admin module.
// Returns a zero-value Module (no service) when pool is nil.
func NewModule(
	pool *pgxpool.Pool,
	accountSvc *account.Service,
	secret string,
	logger *logrus.Logger,
) *Module {
	if pool == nil {
		return &Module{}
	}
	repo := NewRepository(pool)
	svc := NewService(repo, accountSvc, secret, logger)
	return &Module{Service: svc}
}
