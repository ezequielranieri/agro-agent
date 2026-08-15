package store

import (
	"context"

	"github.com/agro-agent/agro-agent/internal/domain"
)

// LoteFilters controla la consulta de lotes. Sin filtro de campaña/cultivo
// no se hace join con campana_lotes (evita duplicados por campaña).
type LoteFilters struct {
	LoteCodigo    string
	CampanaNombre string
	Cultivo       string
}

// LoteStore es el puerto de consulta de lotes.
type LoteStore interface {
	ListLotes(ctx context.Context, tenantID domain.TenantID, f LoteFilters) ([]domain.Lote, error)
	// ListLotesConCampanaActual devuelve cada lote UNA sola vez con el join de
	// su campaña de mayor id (la "actual") y su cultivo. Vive como método
	// aparte —y no como variante de ListLotes— porque los contratos difieren:
	// la tool consultar_lotes exige NO unir sin filtros (evita duplicados por
	// campaña), mientras que el endpoint de lotes SIEMPRE quiere la campaña
	// vigente por lote.
	ListLotesConCampanaActual(ctx context.Context, tenantID domain.TenantID) ([]domain.Lote, error)
}