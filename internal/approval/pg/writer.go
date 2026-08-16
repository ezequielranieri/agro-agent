package pg

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/agro-agent/agro-agent/internal/approval"
	"github.com/agro-agent/agro-agent/internal/domain"
)

// queryRower es la mínima interfaz que comparten *pgxpool.Pool y pgx.Tx para
// resolver una fila. Permite que la re-validación corra DENTRO de la
// transacción de aprobación (misma conexión que el decide y el insert).
type queryRower interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Applier implementa el puerto approval.Applier: la aprobación completa
// (resolver + decidir + insertar) corre en UNA transacción. Usa el pool para
// tomarla del grupo; recién commitea cuando todo salió bien.
type Applier struct {
	pool *pgxpool.Pool
}

func NewApplier(pool *pgxpool.Pool) *Applier {
	return &Applier{pool: pool}
}

// Apply ejecuta la aprobación en UNA transacción. Orden deliberado:
//  1. Re-validación del contexto dentro del tx (los resolvers acotan al
//     tenant: un lote de otra cooperativa no resuelve acá).
//  2. DECISIÓN CONDICIONAL (WHERE status='pendiente'): decide quién gana la
//     carrera. El perdedor obtiene RowsAffected=0 → ErrNotPending (409 en
//     HTTP) y su transacción hace rollback.
//  3. INSERT de la aplicación en el mismo tx (solo el ganador llega).
//  4. Commit: solo el ganador persiste.
//
// Sin la guarda condicional, dos approves concurrentes con el mismo token
// válido pasaban las lecturas y duplicaban la fila de aplicación.
func (a *Applier) Apply(ctx context.Context, tid domain.TenantID, id, decidedBy int64, p approval.AplicacionPayload) (domain.Aplicacion, error) {
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return domain.Aplicacion{}, fmt.Errorf("pg: comenzar transacción de aprobación: %w", err)
	}
	// Si Apply retorna sin Commit, el rollback descarta decisión e insert.
	// El error del rollback se ignora a propósito: el error real ya viaja.
	defer func() { _ = tx.Rollback(ctx) }()

	loteID, err := resolveID(ctx, tx, "lotes", "codigo", tid, p.LoteCodigo)
	if err != nil {
		return domain.Aplicacion{}, fmt.Errorf("re-validación: el lote %q no existe o no pertenece al tenant", p.LoteCodigo)
	}
	productoID, err := resolveID(ctx, tx, "productos", "nombre", tid, p.Producto)
	if err != nil {
		return domain.Aplicacion{}, fmt.Errorf("re-validación: el producto %q no existe o no pertenece al tenant", p.Producto)
	}
	campanaID, err := resolveID(ctx, tx, "campanas", "nombre", tid, p.Campana)
	if err != nil {
		return domain.Aplicacion{}, fmt.Errorf("re-validación: la campaña %q no existe o no pertenece al tenant", p.Campana)
	}

	// Guarda de carrera (TOCTOU): la transición solo vale sobre una fila
	// 'pendiente'. Si otra aprobación ganó antes, esta fila ya no es pendiente
	// y la fila 0 de RowsAffected tira abajo la transacción (sin insert).
	tag, err := tx.Exec(ctx, `
UPDATE approval_requests
SET status = $3, decided_by = $4, decided_at = now(),
    executed_at = CASE WHEN $3 = 'ejecutado' THEN now() ELSE executed_at END
WHERE tenant_id = $1 AND id = $2 AND status = 'pendiente'`,
		tid, id, approval.StatusExecuted, decidedBy,
	)
	if err != nil {
		return domain.Aplicacion{}, fmt.Errorf("pg: decidir solicitud: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return domain.Aplicacion{}, approval.ErrNotPending
	}

	fecha, err := time.Parse("2006-01-02", p.FechaPlanificada)
	if err != nil {
		return domain.Aplicacion{}, fmt.Errorf("pg: fecha_planificada inválida: %w", err)
	}
	var appID int64
	err = tx.QueryRow(ctx, `
INSERT INTO aplicaciones (tenant_id, lote_id, campana_id, producto_id, estado,
                          dosis, unidad_dosis, fecha_planificada, notas)
VALUES ($1, $2, $3, $4, 'planificada', $5, $6, $7, $8)
RETURNING id`,
		tid, loteID, campanaID, productoID, p.Dosis, p.UnidadDosis, fecha, p.Notas,
	).Scan(&appID)
	if err != nil {
		return domain.Aplicacion{}, fmt.Errorf("pg: crear aplicación: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return domain.Aplicacion{}, fmt.Errorf("pg: commit de aprobación: %w", err)
	}

	// Sin join a propósito: es la fila recién creada. La lectura completa con
	// lote/campaña/producto (nombres) sale por ListAplicaciones, el puerto
	// existente de consulta.
	return domain.Aplicacion{
		ID:               appID,
		TenantID:         tid,
		LoteID:           loteID,
		CampanaID:        campanaID,
		Producto:         "",
		ProductoTipo:     "",
		CampanaNombre:    "",
		LoteCodigo:       "",
		Estado:           "planificada",
		Dosis:            p.Dosis,
		UnidadDosis:      p.UnidadDosis,
		FechaPlanificada: &fecha,
		Notas:            p.Notas,
	}, nil
}

// resolveID traduce un identificador de negocio (código de lote, nombre de
// producto/campaña) a su ID, SIEMPRE acotado al tenant. Acepta pool o tx:
// la re-validación del approve corre dentro de la transacción de aprobación.
// Los nombres de tabla/columna salen de constantes locales (nunca de input):
// no hay lugar para inyección SQL.
func resolveID(ctx context.Context, q queryRower, table, column string, tid domain.TenantID, value string) (int64, error) {
	var id int64
	err := q.QueryRow(ctx,
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
	pool *pgxpool.Pool
}

func NewAuditor(pool *pgxpool.Pool) *Auditor {
	return &Auditor{pool: pool}
}

func (a *Auditor) Record(ctx context.Context, tid domain.TenantID, actorID int64, action, tool string, params, result any) error {
	// pgx serializa a jsonb cualquier valor JSON-marshalable: un json.RawMessage
	// (el payload ya codificado) entra directo; un struct (la aplicación creada)
	// se marshalea en el camino.
	_, err := a.pool.Exec(ctx, `
INSERT INTO audit_log (tenant_id, user_id, action, tool, params, result)
VALUES ($1, $2, $3, $4, $5, $6)`,
		tid, actorID, action, tool, params, result,
	)
	if err != nil {
		return fmt.Errorf("pg: registrar auditoría: %w", err)
	}
	return nil
}
