-- =============================================================================
-- Migración incremental · v2 — tabla approval_requests (HITL)
-- Para aplicar sobre una DB existente con el schema v1:
--   psql "$AGRO_DATABASE_URL" -f db/migrations/001_approval_requests.sql
-- Los IF NOT EXISTS la hacen idempotente (aplicarla dos veces no rompe nada).
-- =============================================================================

BEGIN;

CREATE TABLE IF NOT EXISTS approval_requests (
    id            BIGSERIAL PRIMARY KEY,
    tenant_id     BIGINT      NOT NULL REFERENCES tenants(id),
    actor_user_id BIGINT      NOT NULL,
    action        TEXT        NOT NULL,             -- ej: 'programar_aplicacion'
    payload       JSONB       NOT NULL,             -- params validados de la tool
    status        TEXT        NOT NULL DEFAULT 'pendiente'
                  CHECK (status IN ('pendiente','aprobado','rechazado','ejecutado','vencido')),
    token_hash    TEXT        NOT NULL,             -- sha256 hex del token (nunca el token plano)
    expires_at    TIMESTAMPTZ NOT NULL,             -- la solicitud muere sola
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    decided_by    BIGINT,                           -- quien decidió (aprobó/rechazó)
    decided_at    TIMESTAMPTZ,
    executed_at   TIMESTAMPTZ,
    UNIQUE (tenant_id, id),                         -- consistencia con FKs compuestas
    CONSTRAINT approval_requests_actor_fk
        FOREIGN KEY (tenant_id, actor_user_id) REFERENCES users(tenant_id, id),
    CONSTRAINT approval_requests_decider_fk
        FOREIGN KEY (tenant_id, decided_by) REFERENCES users(tenant_id, id)
);

CREATE INDEX IF NOT EXISTS idx_approvals_tenant_status
    ON approval_requests (tenant_id, status, created_at);

COMMIT;
