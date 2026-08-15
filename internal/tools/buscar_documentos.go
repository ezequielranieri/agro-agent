package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/agro-agent/agro-agent/internal/embedding"
	"github.com/agro-agent/agro-agent/internal/store"
	"github.com/agro-agent/agro-agent/internal/tenant"
)

// BuscarDocumentosParams es el contrato tipado del tool RAG.
type BuscarDocumentosParams struct {
	Query  string `json:"query"`
	Limite int    `json:"limite"`
}

func (p BuscarDocumentosParams) validate() error {
	if strings.TrimSpace(p.Query) == "" {
		return fmt.Errorf("query es requerida")
	}
	// limite 0 = sin especificar (default 3); solo se rechaza el rango inválido.
	if p.Limite < 0 || p.Limite > 5 {
		return fmt.Errorf("limite inválido %d (esperado 1..5)", p.Limite)
	}
	return nil
}

// DocumentoResult es lo que la tool devuelve al LLM: el fragmento + su fuente.
// La proyección NO incluye metadata cruda (puede contener JSON anidado que
// ensucia el contexto); expone lo que el modelo necesita para responder.
type DocumentoResult struct {
	Filename string  `json:"filename"`
	Content  string  `json:"content"`
	Score    float64 `json:"score"`
}

// BuscarDocumentos construye la tool de RAG sobre el puerto DocumentoStore y
// el puerto Embedder. Flujo: valida → tenant del contexto → embedding de la
// consulta → top-k por similitud cosena dentro del tenant.
//
// El LLM controla la QUERY, jamás el tenant ni el embedding (que se genera
// server-side). La búsqueda es de solo lectura: no hay HITL ni escritura.
func BuscarDocumentos(docs store.DocumentoStore, emb embedding.Embedder) Tool {
	return Tool{
		Name:        "buscar_documentos",
		Description: "Busca documentos técnicos de la cooperativa (manuales de buenas prácticas, protocolos de aplicación, informes de campaña) relevantes para la consulta. Úsala cuando la respuesta requiere procedimientos, recomendaciones o información documental que NO está en lotes, aplicaciones ni rendimientos. Devuelve los fragmentos más relevantes con su archivo de origen.",
		ParamsSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query":  map[string]any{"type": "string", "description": "La consulta o tema a buscar en los documentos, en lenguaje natural."},
				"limite": map[string]any{"type": "integer", "description": "Cantidad máxima de documentos a devolver (1..5). Opcional, default 3."},
			},
			"additionalProperties": false,
		},
		Run: func(ctx context.Context, raw json.RawMessage) (Result, error) {
			var p BuscarDocumentosParams
			dec := json.NewDecoder(strings.NewReader(string(raw)))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&p); err != nil {
				return Result{}, fmt.Errorf("buscar_documentos: params inválidos: %w", err)
			}
			if err := p.validate(); err != nil {
				return Result{}, err
			}
			if p.Limite == 0 {
				p.Limite = 3 // default del schema
			}

			// El tenant SIEMPRE sale del contexto, nunca del input del LLM.
			tid, err := tenant.FromContext(ctx)
			if err != nil {
				return Result{}, fmt.Errorf("buscar_documentos: %w", err)
			}

			// El embedding se genera server-side con el puerto Embedder.
			// Nunca confiamos en un vector enviado por el LLM.
			vec, err := emb.Embed(ctx, p.Query)
			if err != nil {
				return Result{}, fmt.Errorf("buscar_documentos: %w", err)
			}

			sims, err := docs.BuscarSimilares(ctx, tid, vec, p.Limite)
			if err != nil {
				return Result{}, fmt.Errorf("buscar_documentos: %w", err)
			}

			out := make([]DocumentoResult, 0, len(sims))
			for _, s := range sims {
				out = append(out, DocumentoResult{
					Filename: s.Filename,
					Content:  s.Content,
					Score:    s.Score,
				})
			}
			return Result{Data: out}, nil
		},
	}
}