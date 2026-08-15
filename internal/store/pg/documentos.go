package pg

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/agro-agent/agro-agent/internal/domain"
	"github.com/agro-agent/agro-agent/internal/store"
)

// DocumentoStore es el adapter pg del puerto RAG. La búsqueda usa el índice
// HNSW sobre la columna vector(768) y el operador <=> (distancia coseno).
type DocumentoStore struct {
	conn *pgx.Conn
}

func NewDocumentoStore(conn *pgx.Conn) *DocumentoStore {
	return &DocumentoStore{conn: conn}
}

func (s *DocumentoStore) ListSinEmbedding(ctx context.Context, tid domain.TenantID) ([]store.Documento, error) {
	rows, err := s.conn.Query(ctx, `
SELECT id, tenant_id, filename, content_text, metadata
FROM documentos
WHERE tenant_id = $1 AND embedding IS NULL
ORDER BY id`, tid)
	if err != nil {
		return nil, fmt.Errorf("pg: listar documentos sin embedding: %w", err)
	}
	defer rows.Close()

	var out []store.Documento
	for rows.Next() {
		var d store.Documento
		var meta []byte
		if err := rows.Scan(&d.ID, &d.TenantID, &d.Filename, &d.Content, &meta); err != nil {
			return nil, fmt.Errorf("pg: scan documento: %w", err)
		}
		if len(meta) > 0 {
			if err := json.Unmarshal(meta, &d.Metadata); err != nil {
				return nil, fmt.Errorf("pg: metadata inválida en documento %d: %w", d.ID, err)
			}
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pg: iteración documentos: %w", err)
	}
	return out, nil
}

func (s *DocumentoStore) GuardarEmbedding(ctx context.Context, tid domain.TenantID, docID int64, vec []float32) error {
	_, err := s.conn.Exec(ctx, `
UPDATE documentos
SET embedding = $3::vector
WHERE tenant_id = $1 AND id = $2`, tid, docID, vectorString(vec))
	if err != nil {
		return fmt.Errorf("pg: guardar embedding del documento %d: %w", docID, err)
	}
	return nil
}

// BuscarSimilares ordena por distancia coseno (<=> 0 = iguales, 2 = opuestos)
// y expone score = 1 - distancia ∈ [0,1]. El filtro de tenant es ineludible:
// el LLM jamás puede buscar fuera de su cooperativa.
func (s *DocumentoStore) BuscarSimilares(ctx context.Context, tid domain.TenantID, vec []float32, limit int) ([]store.DocumentoSimilar, error) {
	rows, err := s.conn.Query(ctx, `
SELECT id, tenant_id, filename, content_text, metadata,
       1 - (embedding <=> $3::vector) AS score
FROM documentos
WHERE tenant_id = $1 AND embedding IS NOT NULL
ORDER BY embedding <=> $3::vector
LIMIT $2`, tid, limit, vectorString(vec))
	if err != nil {
		return nil, fmt.Errorf("pg: búsqueda de documentos: %w", err)
	}
	defer rows.Close()

	var out []store.DocumentoSimilar
	for rows.Next() {
		var d store.DocumentoSimilar
		var meta []byte
		if err := rows.Scan(&d.ID, &d.TenantID, &d.Filename, &d.Content, &meta, &d.Score); err != nil {
			return nil, fmt.Errorf("pg: scan documento similar: %w", err)
		}
		if len(meta) > 0 {
			if err := json.Unmarshal(meta, &d.Metadata); err != nil {
				return nil, fmt.Errorf("pg: metadata inválida en documento %d: %w", d.ID, err)
			}
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pg: iteración documentos similares: %w", err)
	}
	return out, nil
}

// vectorString serializa []float32 al literal de pgvector: '[a,b,c]'. El cast
// explícito ::vector en la query evita ambigüedad de tipos con el parámetro.
func vectorString(vec []float32) string {
	var sb strings.Builder
	sb.WriteByte('[')
	for i, v := range vec {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(fmt.Sprintf("%f", v))
	}
	sb.WriteByte(']')
	return sb.String()
}