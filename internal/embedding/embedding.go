// Package embedding define el PUERTO del generador de embeddings y su adapter
// Gemini. El RAG lo usa en dos momentos: cmd/embed para indexar documentos y
// la tool buscar_documentos para embeddear la consulta del usuario.
//
// Regla de desacoplamiento: la tool y el cmd hablan con Embedder, nunca con
// Gemini. Los tests usan un fake.
package embedding

import "context"

// Embedder convierte texto en un vector de floats. El vector se guarda en
// Postgres (columna vector(768)) y se busca por similitud coseno.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}
