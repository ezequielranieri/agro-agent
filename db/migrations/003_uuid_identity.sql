-- =============================================================================
-- Migración incremental · v4 — identidad UUID (compatibilidad con agro-iam)
-- Para aplicar sobre una DB existente con schema v1+v2+v3:
--   psql "$AGRO_DATABASE_URL" -f db/migrations/003_uuid_identity.sql
--
-- agro-iam emite JWTs con tenant_id y sub como UUID (RFC 4122, lowercase).
-- agro-agent mantiene el id interno BIGINT como identidad de negocio: NO se
-- migran las columnas tenant_id BIGINT de las 13 tablas. Estas columnas uuid
-- son el MAPPING por el que el middleware traduce el claim del token al id
-- interno (ResolveTenantByUUID / ResolveUserByUUID).
--
-- Idempotente: ADD COLUMN IF NOT EXISTS permite aplicarla dos veces sin romper.
-- =============================================================================

BEGIN;

-- gen_random_uuid() viene de pgcrypto (schema.sql ya la carga en DB nuevas);
-- en una DB migrada la aseguramos por si el schema original no la tenía.
CREATE EXTENSION IF NOT EXISTS pgcrypto;

ALTER TABLE tenants ADD COLUMN IF NOT EXISTS uuid uuid NOT NULL UNIQUE DEFAULT gen_random_uuid();
ALTER TABLE users   ADD COLUMN IF NOT EXISTS uuid uuid NOT NULL UNIQUE DEFAULT gen_random_uuid();

-- Pin de los ids demo: db/seed.sql usa estos UUIDs FIJOS para que
-- `cmd/mktoken -uuid` pueda emitir tokens agro-iam-style que resuelvan. En una
-- DB existente, las filas demo se re-ajustan acá para mantener la invariante
-- "tenant 1 → 11111111-… y user 2 → 22222222-…" consistente entre DB nueva
-- (initdb) y DB migrada. IS DISTINCT FROM hace el UPDATE idempotente.
UPDATE tenants SET uuid = '11111111-1111-4111-8111-111111111111'
WHERE id = 1 AND uuid IS DISTINCT FROM '11111111-1111-4111-8111-111111111111';

UPDATE users SET uuid = '22222222-2222-4222-8222-222222222222'
WHERE id = 2 AND uuid IS DISTINCT FROM '22222222-2222-4222-8222-222222222222';

COMMIT;