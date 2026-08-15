// Package pg es el adaptador PostgreSQL del puerto Store de approvals.
// Regla de oro (igual que internal/store/pg): el WHERE de tenant SIEMPRE
// viene del parámetro tid (del contexto), nunca de un input controlable.
package pg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/agro-agent/agro-agent/internal/approval"
	"github.com/agro-agent/agro-agent/internal/domain"
)

// ApprovalStore persiste solicitudes de aprobación. La DB guarda el token
// SOLO como hash: este adapter jamás lee ni escribe el token plano.
type ApprovalStore struct {
	conn *pgx.Conn
}

func NewApprovalStore(conn *pgx.Conn) *ApprovalStore {
	return &ApprovalStore{conn: conn}
}

func (s *ApprovalStore) Create(ctx context.Context, tid domain.TenantID, actorID int64, action string, payload json.RawMessage, tokenHash string, expiresAt time.Time) (int64, error) {
	// $1 SIEMPRE es el tenant. token_hash (no el token): la DB no puede
	// filtrar el secreto ni siquiera ante una fuga de datos.
	var id int64
	err := s.conn.QueryRow(ctx, `
INSERT INTO approval_requests (tenant_id, actor_user_id, action, payload, token_hash, expires_at)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id`,
		tid, actorID, action, payload, tokenHash, expiresAt,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("pg: crear solicitud: %w", err)
	}
	return id, nil
}

func (s *ApprovalStore) GetByTenant(ctx context.Context, tid domain.TenantID, id int64) (*approval.Request, error) {
	var r approval.Request
	// payload viene como json.RawMessage (jsonb), los campos nullable como
	// punteros: pgx mapea NULL a nil sin error.
	err := s.conn.QueryRow(ctx, `
SELECT id, tenant_id, actor_user_id, action, payload, status, token_hash,
       expires_at, created_at, decided_by, decided_at, executed_at
FROM approval_requests
WHERE tenant_id = $1 AND id = $2`,
		tid, id,
	).Scan(
		&r.ID, &r.TenantID, &r.ActorUserID, &r.Action, &r.Payload, &r.Status,
		&r.TokenHash, &r.ExpiresAt, &r.CreatedAt, &r.DecidedBy, &r.DecidedAt, &r.ExecutedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, approval.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("pg: buscar solicitud: %w", err)
	}
	return &r, nil
}

func (s *ApprovalStore) ListByTenant(ctx context.Context, tid domain.TenantID, status string) ([]approval.Request, error) {
	query := `
SELECT id, tenant_id, actor_user_id, action, payload, status, token_hash,
       expires_at, created_at, decided_by, decided_at, executed_at
FROM approval_requests
WHERE tenant_id = $1`
	args := []any{tid}
	if status != "" {
		// El status lo validan el service/tool/HTTP antes de llegar acá:
		// nunca arma SQL desde el input del cliente.
		args = append(args, status)
		query += fmt.Sprintf(" AND status = $%d", len(args))
	}
	query += ` ORDER BY created_at DESC`

	rows, err := s.conn.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("pg: listar solicitudes: %w", err)
	}
	defer rows.Close()

	var out []approval.Request
	for rows.Next() {
		var r approval.Request
		if err := rows.Scan(
			&r.ID, &r.TenantID, &r.ActorUserID, &r.Action, &r.Payload, &r.Status,
			&r.TokenHash, &r.ExpiresAt, &r.CreatedAt, &r.DecidedBy, &r.DecidedAt, &r.ExecutedAt,
		); err != nil {
			return nil, fmt.Errorf("pg: scan de solicitud: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pg: iteración de solicitudes: %w", err)
	}
	return out, nil
}

func (s *ApprovalStore) MarkExpired(ctx context.Context, tid domain.TenantID) (int, error) {
	// La solicitud "muere sola": la marca vencida la hace el reloj de la DB
	// (expires_at < now()) para que no queden pendientes zombies.
	tag, err := s.conn.Exec(ctx, `
UPDATE approval_requests
SET status = 'vencido'
WHERE tenant_id = $1 AND status = 'pendiente' AND expires_at < now()`,
		tid,
	)
	if err != nil {
		return 0, fmt.Errorf("pg: marcar vencidas: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

func (s *ApprovalStore) Decide(ctx context.Context, tid domain.TenantID, id, decidedBy int64, status approval.Status) error {
	tag, err := s.conn.Exec(ctx, `
UPDATE approval_requests
SET status = $3, decided_by = $4, decided_at = now(),
    executed_at = CASE WHEN $3 = 'ejecutado' THEN now() ELSE executed_at END
WHERE tenant_id = $1 AND id = $2`,
		tid, id, status, decidedBy,
	)
	if err != nil {
		return fmt.Errorf("pg: decidir solicitud: %w", err)
	}
	if tag.RowsAffected() != 1 {
		// La fila no existe (o es de otro tenant): la solicitud no está en juego.
		return approval.ErrNotFound
	}
	return nil
}