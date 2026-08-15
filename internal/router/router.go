// Package router clasifica la consulta del usuario por dominio (datos
// estructurados vs documentos) para exponer al LLM solo las tools
// relevantes. Es un sesgo no barrera: si la clasificación es incierta, el
// agente expone todas las tools.
package router

import (
	"context"

	"github.com/agro-agent/agro-agent/internal/tools"
)

// Clasificador decide qué dominios son relevantes para una consulta.
// Devuelve la lista de dominios a exponer: [datos], [documentos],
// [datos documentos], o [] (indefinido → exponer todo).
type Clasificador interface {
	Clasificar(ctx context.Context, consulta string) ([]tools.Dominio, error)
}
