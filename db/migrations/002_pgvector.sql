-- =============================================================================
-- Migración incremental · v3 — RAG con pgvector
-- Para aplicar sobre una DB existente con schema v1+v2:
--   psql "$AGRO_DATABASE_URL" -f db/migrations/002_pgvector.sql
-- Requiere la extensión pgvector instalada (imagen pgvector/pgvector:pg16).
-- Idempotente: IF NOT EXISTS permite aplicarla dos veces sin romper nada.
-- =============================================================================

BEGIN;

CREATE EXTENSION IF NOT EXISTS vector;

-- Los embeddings viven en la tabla documentos (SOLO documentos, nunca datos
-- estructurados). La columna es nullable: el cmd/embed la llena de forma
-- incremental, y un documento sin embedding simplemente no aparece en el RAG.
ALTER TABLE documentos
    ADD COLUMN IF NOT EXISTS embedding vector(768);

-- Índice HNSW (graph-based, exacto-ish y rápido): la búsqueda es por similitud
-- coseno (<=>). 768 = dimensión de text-embedding-004, el modelo default.
CREATE INDEX IF NOT EXISTS idx_documentos_embedding_hnsw
    ON documentos USING hnsw (embedding vector_cosine_ops);

COMMIT;