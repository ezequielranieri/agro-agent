package store

import (
	"context"

	"github.com/agro-agent/agro-agent/internal/domain"
)

// RendimientoFilters controla la consulta de rendimientos (rinde real).
type RendimientoFilters struct {
	CampanaNombre string
	LoteCodigo    string
}

// RendimientoStore es el puerto de consulta de rendimientos.
type RendimientoStore interface {
	ListRendimientos(ctx context.Context, tenantID domain.TenantID, f RendimientoFilters) ([]domain.Rendimiento, error)
}