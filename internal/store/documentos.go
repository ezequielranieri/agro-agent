package store

import (
	"context"

	"github.com/agro-agent/agro-agent/internal/domain"
)

// Documento es un archivo técnico de la cooperativa (manuales, protocolos,
// informes). El RAG SOLO indexa documentos — nunca datos estructurados.
type Documento struct {
	ID       int64
	TenantID domain.TenantID
	Filename string
	Content  string
	Metadata map[string]any
}

// DocumentoSimilar es el resultado de una búsqueda por similitud coseno.
// Score ∈ [0,1]: 1 = idéntico al vector de la consulta.
type DocumentoSimilar struct {
	Documento
	Score float64
}

// DocumentoStore es el puerto del RAG.
type DocumentoStore interface {
	// ListSinEmbedding devuelve documentos sin vector (para indexar en lote).
	ListSinEmbedding(ctx context.Context, tid domain.TenantID) ([]Documento, error)
	// GuardarEmbedding persiste el vector de un documento (upsert por id).
	GuardarEmbedding(ctx context.Context, tid domain.TenantID, docID int64, vec []float32) error
	// BuscarSimilares devuelve los top-k documentos por similitud coseno,
	// siempre dentro del tenant. Vec debe tener la dimensión de la columna.
	BuscarSimilares(ctx context.Context, tid domain.TenantID, vec []float32, limit int) ([]DocumentoSimilar, error)
}