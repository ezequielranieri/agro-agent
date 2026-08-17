package pg

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/agro-agent/agro-agent/internal/domain"
	"github.com/agro-agent/agro-agent/internal/store"
)

// TenantStore resuelve las identidades UUID que emite agro-iam a los ids
// internos (BIGINT) de la DB. Es el ÚNICO punto que traduce el claim del token
// (tenant_id/sub UUID) al int64 del dominio: el resto del sistema sigue viendo
// TenantID int64 y actor int64, sin migrar las columnas tenant_id.
// Comparte el *pgxpool.Pool con los demás adapters (thread-safe).
type TenantStore struct {
	pool *pgxpool.Pool
}

func NewTenantStore(pool *pgxpool.Pool) *TenantStore {
	return &TenantStore{pool: pool}
}

// ResolveTenantByUUID traduce el tenant UUID de agro-iam al id interno.
// No encontrado → store.ErrNotFound (el middleware lo mapea al 401 uniforme).
func (s *TenantStore) ResolveTenantByUUID(ctx context.Context, uuid string) (domain.TenantID, error) {
	var id domain.TenantID
	err := s.pool.QueryRow(ctx, `SELECT id FROM tenants WHERE uuid = $1`, uuid).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, store.ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("pg: resolver tenant por uuid: %w", err)
	}
	return id, nil
}

// ResolveUserByUUID traduce el sub UUID de agro-iam al id de usuario interno,
// SIEMPRE acotado al tenant del request: un usuario de otra cooperativa no
// resuelve bajo este tenant (defensa cross-tenant). No encontrado → ErrNotFound.
func (s *TenantStore) ResolveUserByUUID(ctx context.Context, tid domain.TenantID, uuid string) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `SELECT id FROM users WHERE uuid = $1 AND tenant_id = $2`, uuid, tid).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, store.ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("pg: resolver usuario por uuid: %w", err)
	}
	return id, nil
}
