package pg

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/agro-agent/agro-agent/internal/approval"
	"github.com/agro-agent/agro-agent/internal/domain"
)

// ApplicationWriter es el ÚNICO punto de INSERT en aplicaciones del slice HITL.
// Se alcanza solo tras aprobar con token válido y re-validar el contexto.
type ApplicationWriter struct {
	conn *pgx.Conn
}

func NewApplicationWriter(conn *pgx.Conn) *ApplicationWriter {
	return &ApplicationWriter{conn: conn}
}

func (w *ApplicationWriter) CreateAplicacion(ctx context.Context, tid domain.TenantID, _ int64, in approval.AplicacionInput) (domain.Aplicacion, error) {
	// El estado SIEMPRE es 'planificada': el HITL planifica; ejecutar es otro
	// flujo futuro (un worker o una acción manual). El actor que aprobó queda
	// registrado en audit_log; ejecutada_por_id es solo para ejecuciones reales.
	fecha, err := time.Parse("2006-01-02", in.FechaPlanificada)
	if err != nil {
		return domain.Aplicacion{}, fmt.Errorf("pg: fecha_planificada inválida: %w", err)
	}

	var id int64
	err = w.conn.QueryRow(ctx, `
INSERT INTO aplicaciones (tenant_id, lote_id, campana_id, producto_id, estado,
                          dosis, unidad_dosis, fecha_planificada, notas)
VALUES ($1, $2, $3, $4, 'planificada', $5, $6, $7, $8)
RETURNING id`,
		tid, in.LoteID, in.CampanaID, in.ProductoID, in.Dosis, in.UnidadDosis, fecha, in.Notas,
	).Scan(&id)
	if err != nil {
		return domain.Aplicacion{}, fmt.Errorf("pg: crear aplicación: %w", err)
	}

	// Sin join a propósito: es la fila recién creada. La lectura completa con
	// lote/campaña/producto (nombres) sale por ListAplicaciones, el puerto
	// existente de consulta.
	return domain.Aplicacion{
		ID:               id,
		TenantID:         tid,
		LoteID:           in.LoteID,
		CampanaID:        in.CampanaID,
		Producto:         "",
		ProductoTipo:     "",
		CampanaNombre:    "",
		LoteCodigo:       "",
		Estado:           "planificada",
		Dosis:            in.Dosis,
		UnidadDosis:      in.UnidadDosis,
		FechaPlanificada: &fecha,
		Notas:            in.Notas,
	}, nil
}

// Resolver traduce códigos/nombres de negocio a IDs, siempre acotado al tenant.
// Un SELECT sin WHERE tenant_id resolvería el lote "12" de OTRA cooperativa.
type Resolver struct {
	conn *pgx.Conn
}

func NewResolver(conn *pgx.Conn) *Resolver {
	return &Resolver{conn: conn}
}

func (r *Resolver) ResolveLoteID(ctx context.Context, tid domain.TenantID, codigo string) (int64, error) {
	return resolveID(ctx, r.conn, "lotes", "codigo", tid, codigo)
}

func (r *Resolver) ResolveProductoID(ctx context.Context, tid domain.TenantID, nombre string) (int64, error) {
	return resolveID(ctx, r.conn, "productos", "nombre", tid, nombre)
}

func (r *Resolver) ResolveCampanaID(ctx context.Context, tid domain.TenantID, nombre string) (int64, error) {
	return resolveID(ctx, r.conn, "campanas", "nombre", tid, nombre)
}

func resolveID(ctx context.Context, conn *pgx.Conn, table, column string, tid domain.TenantID, value string) (int64, error) {
	var id int64
	// Los nombres de tabla/columna salen de constantes locales (nunca de
	// input): no hay lugar para inyección SQL.
	err := conn.QueryRow(ctx,
		fmt.Sprintf(`SELECT id FROM %s WHERE tenant_id = $1 AND %s = $2`, table, column),
		tid, value,
	).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, approval.ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("pg: resolver %s: %w", table, err)
	}
	return id, nil
}

// Auditor registra cada decisión de aprobación en audit_log. Fail-open: el
// service ignora el error y solo loguea WARN, la acción ya está decidida.
type Auditor struct {
	conn *pgx.Conn
}

func NewAuditor(conn *pgx.Conn) *Auditor {
	return &Auditor{conn: conn}
}

func (a *Auditor) Record(ctx context.Context, tid domain.TenantID, actorID int64, action, tool string, params, result any) error {
	// pgx serializa a jsonb cualquier valor JSON-marshalable: un json.RawMessage
	// (el payload ya codificado) entra directo; un struct (la aplicación creada)
	// se marshalea en el camino.
	_, err := a.conn.Exec(ctx, `
INSERT INTO audit_log (tenant_id, user_id, action, tool, params, result)
VALUES ($1, $2, $3, $4, $5, $6)`,
		tid, actorID, action, tool, params, result,
	)
	if err != nil {
		return fmt.Errorf("pg: registrar auditoría: %w", err)
	}
	return nil
}