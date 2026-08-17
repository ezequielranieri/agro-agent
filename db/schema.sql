-- =============================================================================
-- agro-agent · Schema v1 (+ v2: tabla approval_requests para HITL)
-- Sistema multi-tenant de agentes inteligentes para cooperativas agropecuarias.
--
-- PRINCIPIOS
--  1. Multi-tenancy: shared schema + columna tenant_id en TODAS las tablas.
--  2. Unicidad compuesta CON tenant_id: el codigo de lote "12" existe en toda
--     cooperativa. Sin esto, "lote 12" resuelve al lote de OTRA cooperativa.
--  3. FKs compuestas (tenant_id, ...): impiden joins cross-tenant aunque falte
--     el filtro de tenant a nivel de query. Defensa en profundidad.
--  4. Los datos estructurados van por TOOL CALLING determinístico, NUNCA RAG.
--     RAG es solo para documentos (tabla `documentos`).
--  5. Audit log de toda ejecución de tool (rendición de cuentas en coop).
-- =============================================================================

BEGIN;

-- -----------------------------------------------------------------------------
-- Extensiones
-- -----------------------------------------------------------------------------
CREATE EXTENSION IF NOT EXISTS pgcrypto;        -- gen_random_uuid / digest
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- -----------------------------------------------------------------------------
-- Catálogo
-- -----------------------------------------------------------------------------
CREATE TABLE tenants (
    id          BIGSERIAL PRIMARY KEY,
    -- uuid: identidad externa (agro-iam). El middleware traduce el claim
    -- tenant_id UUID del JWT al id BIGINT interno vía esta columna.
    uuid        uuid        NOT NULL UNIQUE DEFAULT gen_random_uuid(),
    name        TEXT        NOT NULL,
    slug        TEXT        NOT NULL UNIQUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- roles: un rol por usuario en este tenant (simplificación consciente).
CREATE TABLE users (
    id            BIGSERIAL PRIMARY KEY,
    -- uuid: identidad externa (agro-iam). El claim sub (UUID) del JWT se
    -- resuelve al id BIGINT interno vía esta columna, acotado al tenant.
    uuid          uuid        NOT NULL UNIQUE DEFAULT gen_random_uuid(),
    tenant_id     BIGINT      NOT NULL REFERENCES tenants(id),
    email         TEXT        NOT NULL,
    display_name  TEXT        NOT NULL,
    role          TEXT        NOT NULL CHECK (role IN ('admin', 'agronomo', 'productor')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, email),
    -- Necesario para las FKs compuestas (tenant_id, id) de lotes/conversations/messages.
    UNIQUE (tenant_id, id)
);

-- -----------------------------------------------------------------------------
-- Núcleo agro
-- -----------------------------------------------------------------------------
-- El lote es la unidad geográfica de producción. `codigo` es lo que el
-- productor menciona en lenguaje natural ("lote 12").
CREATE TABLE lotes (
    id              BIGSERIAL PRIMARY KEY,
    tenant_id       BIGINT      NOT NULL REFERENCES tenants(id),
    codigo          TEXT        NOT NULL,
    nombre          TEXT,
    superficie_ha   NUMERIC(10,2) NOT NULL,
    tipo_suelo      TEXT,                       -- ej: 'franco-arcilloso'
    responsable_id  BIGINT,                     -- agrónomo a cargo
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, codigo),
    -- UNIQUE(tenant_id, id) requerido por las FKs compuestas (aplicaciones,
    -- campana_lotes, rendimientos, registros_clima).
    UNIQUE (tenant_id, id),
    CONSTRAINT lotes_responsable_fk
        FOREIGN KEY (tenant_id, responsable_id) REFERENCES users(tenant_id, id)
);

-- Una campaña es un ciclo productivo. En Argentina coexisten campañas fina
-- (trigo, invierno) y gruesa (soja/maíz, verano) dentro del mismo año nominal.
CREATE TABLE campanas (
    id            BIGSERIAL PRIMARY KEY,
    tenant_id     BIGINT       NOT NULL REFERENCES tenants(id),
    nombre        TEXT         NOT NULL,        -- ej: '2025/2026'
    temporada     TEXT         NOT NULL CHECK (temporada IN ('fina', 'gruesa')),
    fecha_inicio  DATE,
    fecha_fin     DATE,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, nombre, temporada),
    -- UNIQUE(tenant_id, id) requerido por FKs compuestas (campana_lotes,
    -- aplicaciones, rendimientos).
    UNIQUE (tenant_id, id)
);

-- Qué lotes participan de qué campaña, con qué cultivo y el objetivo de rinde.
CREATE TABLE campana_lotes (
    id                   BIGSERIAL PRIMARY KEY,
    tenant_id            BIGINT      NOT NULL REFERENCES tenants(id),
    campana_id           BIGINT      NOT NULL,
    lote_id              BIGINT      NOT NULL,
    cultivo              TEXT        NOT NULL,  -- ej: 'soja', 'trigo', 'maiz'
    rendimiento_objetivo NUMERIC(10,2),         -- tn/ha
    UNIQUE (tenant_id, campana_id, lote_id),
    CONSTRAINT campana_lotes_campana_fk
        FOREIGN KEY (tenant_id, campana_id) REFERENCES campanas(tenant_id, id),
    CONSTRAINT campana_lotes_lote_fk
        FOREIGN KEY (tenant_id, lote_id) REFERENCES lotes(tenant_id, id)
);

-- Catálogo de insumos (agroquímicos / fertilizantes / semillas).
CREATE TABLE productos (
    id               BIGSERIAL PRIMARY KEY,
    tenant_id        BIGINT      NOT NULL REFERENCES tenants(id),
    nombre           TEXT        NOT NULL,
    tipo             TEXT        NOT NULL CHECK (tipo IN
                       ('herbicida','fungicida','insecticida','fertilizante',
                        'semilla','otro')),
    principio_activo TEXT,
    unidad           TEXT        NOT NULL DEFAULT 'L',  -- L, kg, tn
    UNIQUE (tenant_id, nombre),
    -- UNIQUE(tenant_id, id) requerido por FK compuesta de aplicaciones.
    UNIQUE (tenant_id, id)
);

-- -----------------------------------------------------------------------------
-- EL CORAZÓN: aplicaciones
-- Una única tabla con estado. `planificada` → `ejecutada` (o `cancelada`).
-- El retraso sale solo:
--   WHERE estado = 'planificada' AND fecha_planificada < now()
-- -----------------------------------------------------------------------------
CREATE TABLE aplicaciones (
    id                 BIGSERIAL PRIMARY KEY,
    tenant_id          BIGINT      NOT NULL REFERENCES tenants(id),
    lote_id            BIGINT      NOT NULL,
    campana_id         BIGINT      NOT NULL,
    producto_id        BIGINT      NOT NULL,
    estado             TEXT        NOT NULL DEFAULT 'planificada' CHECK (estado IN
                         ('planificada','ejecutada','cancelada')),
    dosis              NUMERIC(10,2) NOT NULL,
    unidad_dosis       TEXT        NOT NULL DEFAULT 'L/ha',
    fecha_planificada  DATE,
    fecha_ejecucion    DATE,
    ejecutada_por_id   BIGINT,                     -- quién la ejecutó
    notas              TEXT,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT aplicaciones_lote_fk
        FOREIGN KEY (tenant_id, lote_id) REFERENCES lotes(tenant_id, id),
    CONSTRAINT aplicaciones_campana_fk
        FOREIGN KEY (tenant_id, campana_id) REFERENCES campanas(tenant_id, id),
    CONSTRAINT aplicaciones_producto_fk
        FOREIGN KEY (tenant_id, producto_id) REFERENCES productos(tenant_id, id),
    CONSTRAINT aplicaciones_ejecutor_fk
        FOREIGN KEY (tenant_id, ejecutada_por_id) REFERENCES users(tenant_id, id)
);

-- Rendimiento real por lote-campaña. No todo lote llega a cosecha, por eso
-- vive aparte del objetivo (que está en campana_lotes).
CREATE TABLE rendimientos (
    id                BIGSERIAL PRIMARY KEY,
    tenant_id         BIGINT       NOT NULL REFERENCES tenants(id),
    campana_id        BIGINT       NOT NULL,
    lote_id           BIGINT       NOT NULL,
    cultivo           TEXT         NOT NULL,
    rendimiento_real  NUMERIC(10,2) NOT NULL,     -- tn/ha
    unidad_rendimiento TEXT        NOT NULL DEFAULT 'tn/ha',
    fecha_cosecha     DATE,
    UNIQUE (tenant_id, campana_id, lote_id),
    CONSTRAINT rendimientos_campana_fk
        FOREIGN KEY (tenant_id, campana_id) REFERENCES campanas(tenant_id, id),
    CONSTRAINT rendimientos_lote_fk
        FOREIGN KEY (tenant_id, lote_id) REFERENCES lotes(tenant_id, id)
);

-- Clima: factor #1 del rendimiento. Simplificación consciente: por lote-fecha
-- (en producción real el clima es por estación/georreferencia).
CREATE TABLE registros_clima (
    id               BIGSERIAL PRIMARY KEY,
    tenant_id        BIGINT      NOT NULL REFERENCES tenants(id),
    lote_id          BIGINT      NOT NULL,
    fecha            DATE        NOT NULL,
    temp_min_c       NUMERIC(5,2),
    temp_max_c       NUMERIC(5,2),
    lluvia_mm        NUMERIC(6,2),
    humedad_rel_pct  NUMERIC(5,2),
    UNIQUE (tenant_id, lote_id, fecha),
    CONSTRAINT registros_clima_lote_fk
        FOREIGN KEY (tenant_id, lote_id) REFERENCES lotes(tenant_id, id)
);

-- -----------------------------------------------------------------------------
-- RAG (SOLO documentos, NUNCA datos estructurados)
-- -----------------------------------------------------------------------------
CREATE TABLE documentos (
    id            BIGSERIAL PRIMARY KEY,
    tenant_id     BIGINT      NOT NULL REFERENCES tenants(id),
    filename      TEXT        NOT NULL,
    content_text  TEXT        NOT NULL,
    metadata      JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- -----------------------------------------------------------------------------
-- Conversación (memoria del agente)
-- -----------------------------------------------------------------------------
CREATE TABLE conversations (
    id          BIGSERIAL PRIMARY KEY,
    tenant_id   BIGINT      NOT NULL REFERENCES tenants(id),
    user_id     BIGINT      NOT NULL,
    title       TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- UNIQUE(tenant_id, id) requerido por FK compuesta de messages.
    UNIQUE (tenant_id, id),
    CONSTRAINT conversations_user_fk
        FOREIGN KEY (tenant_id, user_id) REFERENCES users(tenant_id, id)
);

CREATE TABLE messages (
    id               BIGSERIAL PRIMARY KEY,
    tenant_id        BIGINT      NOT NULL REFERENCES tenants(id),
    conversation_id  BIGINT      NOT NULL,
    role             TEXT        NOT NULL CHECK (role IN ('user','assistant','tool')),
    content          TEXT        NOT NULL,
    tool_calls       JSONB,                       -- tools que disparó el asistente
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT messages_conversation_fk
        FOREIGN KEY (tenant_id, conversation_id) REFERENCES conversations(tenant_id, id)
);

-- -----------------------------------------------------------------------------
-- Audit log: TODA ejecución de tool queda registrada (rendición de cuentas).
-- -----------------------------------------------------------------------------
CREATE TABLE audit_log (
    id           BIGSERIAL PRIMARY KEY,
    tenant_id    BIGINT      NOT NULL REFERENCES tenants(id),
    user_id      BIGINT      NOT NULL,
    action       TEXT        NOT NULL,            -- ej: 'aplicacion.ejecutar'
    tool         TEXT        NOT NULL,            -- nombre del tool
    params       JSONB       NOT NULL,            -- parámetros con que se llamó
    result       JSONB,                           -- resultado (sin datos sensibles)
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- -----------------------------------------------------------------------------
-- v2 · HITL (human-in-the-loop): aprobaciones de acciones de escritura del agente.
-- La tool `programar_aplicacion` NO inserta directo: crea una solicitud
-- PENDIENTE con un token de aprobación (guardado SOLO como hash sha256, nunca
-- plano). Un admin/agronomo aprueba presentando el token; al aprobar se
-- re-valida el contexto y recién entonces se ejecuta la acción.
-- -----------------------------------------------------------------------------
CREATE TABLE approval_requests (
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

CREATE INDEX idx_approvals_tenant_status
    ON approval_requests (tenant_id, status, created_at);

-- -----------------------------------------------------------------------------
-- Índices para las consultas de las tools (el LLM dispara estas queries)
-- -----------------------------------------------------------------------------
CREATE INDEX idx_aplicaciones_lote_campana
    ON aplicaciones (tenant_id, lote_id, campana_id);
CREATE INDEX idx_aplicaciones_ejecucion_fecha
    ON aplicaciones (tenant_id, fecha_ejecucion);
CREATE INDEX idx_aplicaciones_planificada_retraso
    ON aplicaciones (tenant_id, estado, fecha_planificada)
    WHERE estado = 'planificada';
CREATE INDEX idx_rendimientos_campana
    ON rendimientos (tenant_id, campana_id);
CREATE INDEX idx_campana_lotes_lote
    ON campana_lotes (tenant_id, lote_id);
CREATE INDEX idx_clima_lote
    ON registros_clima (tenant_id, lote_id, fecha);
CREATE INDEX idx_documentos_tenant
    ON documentos (tenant_id);
CREATE INDEX idx_messages_conversation
    ON messages (tenant_id, conversation_id);

-- -----------------------------------------------------------------------------
-- v3 · RAG con pgvector: embeddings de documentos
-- Requiere la extensión pgvector (imagen pgvector/pgvector:pg16). La columna
-- es nullable: cmd/embed la llena incrementalmente.
-- -----------------------------------------------------------------------------
CREATE EXTENSION IF NOT EXISTS vector;

ALTER TABLE documentos
    ADD COLUMN embedding vector(768);

-- Índice HNSW para búsqueda por similitud cosena (<=>).
CREATE INDEX idx_documentos_embedding_hnsw
    ON documentos USING hnsw (embedding vector_cosine_ops);

COMMIT;