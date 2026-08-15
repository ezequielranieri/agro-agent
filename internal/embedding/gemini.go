package embedding

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"google.golang.org/genai"
)

// Dimensión del vector: la columna es vector(768) (migración 002_pgvector.sql).
// gemini-embedding-2 emite 3072 dims por defecto, pero soporta
// OutputDimensionality: recortamos a 768 para no agrandar la tabla ni el
// índice HNSW. Si se cambia, cambiar AMBOS (columna + índice).
const Dimension = 768

// DefaultModel es el modelo de embeddings de Gemini usado por defecto.
// Los modelos viejos (text-embedding-004) ya no existen para keys nuevas;
// gemini-embedding-2 es el actual y soporta recorte de dimensionalidad.
const DefaultModel = "gemini-embedding-2"

// Gemini es el adapter del puerto Embedder para Google Gemini. Misma
// convención que internal/llm: apiKey vacía se resuelve del entorno.
type Gemini struct {
	client *genai.Client
	model  string
}

// NewGemini crea el adapter. model vacío usa DefaultModel.
func NewGemini(ctx context.Context, apiKey, model string) (*Gemini, error) {
	client, err := genai.NewClient(ctx, &genai.ClientConfig{APIKey: apiKey})
	if err != nil {
		return nil, fmt.Errorf("embedding: crear cliente Gemini: %w", err)
	}
	if model == "" {
		model = DefaultModel
	}
	return &Gemini{client: client, model: model}, nil
}

// Embed genera el vector del texto. Devuelve siempre Dimension floats; el
// caller debe verificar la longitud contra la columna vector(768) de la DB.
func (g *Gemini) Embed(ctx context.Context, text string) ([]float32, error) {
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("embedding: texto vacío")
	}
	// Recorte de dimensionalidad: el modelo emite 3072 dims, guardamos 768
	// para que coincida con la columna vector(768) y el índice HNSW.
	dims := int32(Dimension)
	resp, err := g.client.Models.EmbedContent(ctx, g.model,
		[]*genai.Content{{Parts: []*genai.Part{{Text: text}}}},
		&genai.EmbedContentConfig{OutputDimensionality: &dims})
	if err != nil {
		return nil, fmt.Errorf("embedding: Gemini: %w", err)
	}
	if resp == nil || len(resp.Embeddings) == 0 {
		return nil, errors.New("embedding: Gemini no devolvió vectores")
	}
	return resp.Embeddings[0].Values, nil
}