package pg

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/agro-agent/agro-agent/internal/domain"
	"github.com/agro-agent/agro-agent/internal/store"
)

// RendimientoStore usa un *pgxpool.Pool (thread-safe) como todos los
// adapters: las lecturas concurrentes del frontend no comparten un único
// *pgx.Conn y por lo tanto no se pisan entre sí.
type RendimientoStore struct {
	pool *pgxpool.Pool
}

func NewRendimientoStore(pool *pgxpool.Pool) *RendimientoStore {
	return &RendimientoStore{pool: pool}
}

func (s *RendimientoStore) ListRendimientos(ctx context.Context, tid domain.TenantID, f store.RendimientoFilters) ([]domain.Rendimiento, error) {
	query := `
SELECT r.id, r.tenant_id, r.campana_id, c.nombre,
       r.lote_id, l.codigo,
       r.cultivo, r.rendimiento_real, r.unidad_rendimiento, r.fecha_cosecha
FROM rendimientos r
JOIN campanas c ON c.tenant_id = r.tenant_id AND c.id = r.campana_id
JOIN lotes l ON l.tenant_id = r.tenant_id AND l.id = r.lote_id
WHERE r.tenant_id = $1`
	args := []any{tid}

	if f.CampanaNombre != "" {
		args = append(args, f.CampanaNombre)
		query += fmt.Sprintf(" AND c.nombre = $%d", len(args))
	}
	if f.LoteCodigo != "" {
		args = append(args, f.LoteCodigo)
		query += fmt.Sprintf(" AND l.codigo = $%d", len(args))
	}
	query += ` ORDER BY r.campana_id, r.lote_id`

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("pg: consulta de rendimientos: %w", err)
	}
	defer rows.Close()

	var rends []domain.Rendimiento
	for rows.Next() {
		var r domain.Rendimiento
		if err := rows.Scan(
			&r.ID, &r.TenantID, &r.CampanaID, &r.CampanaNombre,
			&r.LoteID, &r.LoteCodigo,
			&r.Cultivo, &r.RendimientoReal, &r.UnidadRendimiento, &r.FechaCosecha,
		); err != nil {
			return nil, fmt.Errorf("pg: scan de rendimiento: %w", err)
		}
		rends = append(rends, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pg: iteración de rendimientos: %w", err)
	}
	return rends, nil
}