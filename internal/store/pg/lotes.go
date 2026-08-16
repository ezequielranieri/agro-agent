package pg

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/agro-agent/agro-agent/internal/domain"
	"github.com/agro-agent/agro-agent/internal/store"
)

// LoteStore comparte un *pgxpool.Pool con los demás adapters: el pool es
// thread-safe y cada Query toma una conexión libre, lo que evita el choque
// de "conn busy" cuando lotes/aplicaciones/approvals corren en paralelo.
type LoteStore struct {
	pool *pgxpool.Pool
}

func NewLoteStore(pool *pgxpool.Pool) *LoteStore {
	return &LoteStore{pool: pool}
}

func (s *LoteStore) ListLotes(ctx context.Context, tid domain.TenantID, f store.LoteFilters) ([]domain.Lote, error) {
	// Sin filtro de campaña/cultivo NO se hace join: cada lote aparece una vez.
	query := `
SELECT l.id, l.tenant_id, l.codigo, l.nombre, l.superficie_ha, l.tipo_suelo, l.responsable_id
FROM lotes l
WHERE l.tenant_id = $1`
	args := []any{tid}

	if f.CampanaNombre != "" || f.Cultivo != "" {
		query = `
SELECT l.id, l.tenant_id, l.codigo, l.nombre, l.superficie_ha, l.tipo_suelo, l.responsable_id,
       c.nombre, cl.cultivo
FROM lotes l
JOIN campana_lotes cl ON cl.tenant_id = l.tenant_id AND cl.lote_id = l.id
JOIN campanas c ON c.tenant_id = cl.tenant_id AND c.id = cl.campana_id
WHERE l.tenant_id = $1`
	}
	if f.LoteCodigo != "" {
		args = append(args, f.LoteCodigo)
		query += fmt.Sprintf(" AND l.codigo = $%d", len(args))
	}
	if f.CampanaNombre != "" {
		args = append(args, f.CampanaNombre)
		query += fmt.Sprintf(" AND c.nombre = $%d", len(args))
	}
	if f.Cultivo != "" {
		args = append(args, f.Cultivo)
		query += fmt.Sprintf(" AND cl.cultivo = $%d", len(args))
	}
	query += ` ORDER BY l.codigo`

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("pg: consulta de lotes: %w", err)
	}
	defer rows.Close()

	var lotes []domain.Lote
	for rows.Next() {
		var l domain.Lote
		if f.CampanaNombre != "" || f.Cultivo != "" {
			if err := rows.Scan(
				&l.ID, &l.TenantID, &l.Codigo, &l.Nombre, &l.SuperficieHa, &l.TipoSuelo, &l.ResponsableID,
				&l.CampanaNombre, &l.Cultivo,
			); err != nil {
				return nil, fmt.Errorf("pg: scan de lote: %w", err)
			}
		} else {
			if err := rows.Scan(
				&l.ID, &l.TenantID, &l.Codigo, &l.Nombre, &l.SuperficieHa, &l.TipoSuelo, &l.ResponsableID,
			); err != nil {
				return nil, fmt.Errorf("pg: scan de lote: %w", err)
			}
		}
		lotes = append(lotes, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pg: iteración de lotes: %w", err)
	}
	return lotes, nil
}

// ListLotesConCampanaActual devuelve los lotes del tenant con la campaña de
// mayor id (la actual) y su cultivo. Un LATERAL con ORDER BY campana_id DESC
// LIMIT 1 elige la campaña vigente por lote sin duplicar filas; el id es el
// orden real de carga de campañas (la actual se crea con el id más alto). Es
// un método aparte para NO tocar el contrato de ListLotes: la tool
// consultar_lotes sigue con su regla de no unir sin filtros.
func (s *LoteStore) ListLotesConCampanaActual(ctx context.Context, tid domain.TenantID) ([]domain.Lote, error) {
	query := `
SELECT l.id, l.tenant_id, l.codigo, l.nombre, l.superficie_ha, l.tipo_suelo, l.responsable_id,
       COALESCE(c.nombre, ''), COALESCE(cl.cultivo, '')
FROM lotes l
LEFT JOIN LATERAL (
    SELECT cl2.campana_id, cl2.cultivo
    FROM campana_lotes cl2
    WHERE cl2.tenant_id = l.tenant_id AND cl2.lote_id = l.id
    ORDER BY cl2.campana_id DESC
    LIMIT 1
) cl ON TRUE
LEFT JOIN campanas c ON c.tenant_id = l.tenant_id AND c.id = cl.campana_id
WHERE l.tenant_id = $1
ORDER BY l.codigo`
	rows, err := s.pool.Query(ctx, query, tid)
	if err != nil {
		return nil, fmt.Errorf("pg: consulta de lotes con campaña actual: %w", err)
	}
	defer rows.Close()

	var lotes []domain.Lote
	for rows.Next() {
		var l domain.Lote
		// COALESCE arriba: un lote sin campana_lotes queda con string vacío,
		// jamás NULL al scan (los campos del domain no son punteros).
		if err := rows.Scan(
			&l.ID, &l.TenantID, &l.Codigo, &l.Nombre, &l.SuperficieHa, &l.TipoSuelo, &l.ResponsableID,
			&l.CampanaNombre, &l.Cultivo,
		); err != nil {
			return nil, fmt.Errorf("pg: scan de lote: %w", err)
		}
		lotes = append(lotes, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pg: iteración de lotes: %w", err)
	}
	return lotes, nil
}