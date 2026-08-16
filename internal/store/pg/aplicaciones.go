// Package pg es el adaptador PostgreSQL del puerto AplicacionStore.
// Regla de oro: el WHERE de tenant SIEMPRE viene del parámetro tid (que el
// handler obtuvo del contexto), nunca de un filtro controlable por el LLM.
package pg

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/agro-agent/agro-agent/internal/domain"
	"github.com/agro-agent/agro-agent/internal/store"
)

// AplicacionStore usa un *pgxpool.Pool, NO un *pgx.Conn: el pool reparte cada
// llamada entre varias conexiones y así los stores sobreviven al paralelismo
// real del HTTP server (una *pgx.Conn única chocaría con "conn busy").
type AplicacionStore struct {
	pool *pgxpool.Pool
}

func NewAplicacionStore(pool *pgxpool.Pool) *AplicacionStore {
	return &AplicacionStore{pool: pool}
}

func (s *AplicacionStore) ListAplicaciones(ctx context.Context, tid domain.TenantID, f store.AplicacionFilters) ([]domain.Aplicacion, error) {
	// $1 SIEMPRE es el tenant. El resto de los filtros son opcionales y
	// controlados por el LLM, pero el tenant jamás viene del input.
	query := `
SELECT a.id, a.tenant_id, a.lote_id, l.codigo,
       a.campana_id, c.nombre, c.temporada,
       p.nombre, p.tipo,
       a.estado, a.dosis, a.unidad_dosis,
       a.fecha_planificada, a.fecha_ejecucion,
       COALESCE(a.notas, '')  -- notas es NULL en DB; el contrato lo expone como texto vacío
FROM aplicaciones a
JOIN lotes l      ON l.tenant_id = a.tenant_id AND l.id = a.lote_id
JOIN campanas c   ON c.tenant_id = a.tenant_id AND c.id = a.campana_id
JOIN productos p  ON p.tenant_id = a.tenant_id AND p.id = a.producto_id
WHERE a.tenant_id = $1`
	args := []any{tid}

	if f.LoteCodigo != "" {
		args = append(args, f.LoteCodigo)
		query += fmt.Sprintf(" AND l.codigo = $%d", len(args))
	}
	if f.CampanaNombre != "" {
		args = append(args, f.CampanaNombre)
		query += fmt.Sprintf(" AND c.nombre = $%d", len(args))
	}
	if f.Temporada != "" {
		args = append(args, f.Temporada)
		query += fmt.Sprintf(" AND c.temporada = $%d", len(args))
	}
	if f.Estado != "" {
		args = append(args, f.Estado)
		query += fmt.Sprintf(" AND a.estado = $%d", len(args))
	}
	if f.Desde != nil {
		args = append(args, *f.Desde)
		query += fmt.Sprintf(" AND a.fecha_planificada >= $%d", len(args))
	}
	if f.Hasta != nil {
		args = append(args, *f.Hasta)
		query += fmt.Sprintf(" AND a.fecha_planificada <= $%d", len(args))
	}
	if f.EjecutadaDesde != nil {
		args = append(args, *f.EjecutadaDesde)
		query += fmt.Sprintf(" AND a.fecha_ejecucion >= $%d", len(args))
	}
	if f.EjecutadaHasta != nil {
		args = append(args, *f.EjecutadaHasta)
		query += fmt.Sprintf(" AND a.fecha_ejecucion <= $%d", len(args))
	}

	query += ` ORDER BY a.fecha_planificada NULLS LAST, a.id`

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("pg: consulta de aplicaciones: %w", err)
	}
	defer rows.Close()

	var apps []domain.Aplicacion
	for rows.Next() {
		var a domain.Aplicacion
		if err := rows.Scan(
			&a.ID, &a.TenantID, &a.LoteID, &a.LoteCodigo,
			&a.CampanaID, &a.CampanaNombre, &a.CampanaTemporada,
			&a.Producto, &a.ProductoTipo,
			&a.Estado, &a.Dosis, &a.UnidadDosis,
			&a.FechaPlanificada, &a.FechaEjecucion, &a.Notas,
		); err != nil {
			return nil, fmt.Errorf("pg: scan de aplicación: %w", err)
		}
		apps = append(apps, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pg: iteración de aplicaciones: %w", err)
	}
	return apps, nil
}