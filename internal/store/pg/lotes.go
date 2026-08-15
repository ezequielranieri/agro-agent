package pg

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/agro-agent/agro-agent/internal/domain"
	"github.com/agro-agent/agro-agent/internal/store"
)

type LoteStore struct {
	conn *pgx.Conn
}

func NewLoteStore(conn *pgx.Conn) *LoteStore {
	return &LoteStore{conn: conn}
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

	rows, err := s.conn.Query(ctx, query, args...)
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