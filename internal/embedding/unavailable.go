package embedding

import (
	"context"
	"errors"
)

// Unavailable es un Embedder que siempre falla con un error descriptivo.
// Se instala en el boot cuando no hay GEMINI_API_KEY (modo solo-Groq): el RAG
// no puede embeddear sin un modelo de embeddings, pero el chat no depende de
// él y arranca igual. El error nunca se silencia: el usuario ve el motivo.
type Unavailable struct{}

func (Unavailable) Embed(_ context.Context, _ string) ([]float32, error) {
	return nil, errors.New("embedding: no hay proveedor de embeddings configurado (falta GEMINI_API_KEY)")
}
