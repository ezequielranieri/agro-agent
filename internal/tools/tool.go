// Package tools ES EL PRODUCTO: el registro de herramientas que el agente
// puede invocar. Cada tool es un contrato tipado con:
//   - Un schema JSON (ParamsSchema) que se envía al LLM para tool calling.
//   - Un Run que recibe JSON crudo, valida contra params tipados, y ejecuta
//     dentro del TenantID del contexto.
//
// Regla de seguridad: el LLM controla los filtros de negocio, pero JAMÁS el
// tenant. El tenant sale del contexto (internal/tenant), no del input.
package tools

import (
	"context"
	"encoding/json"
)

// Dominio es la fuente de verdad que alimenta una tool: datos estructurados
// de la DB o documentos (RAG). El router de discernimiento (internal/router)
// clasifica la consulta por dominio y expone al LLM solo las tools de esos
// dominios; la descripción de cada tool refuerza la misma frontera.
type Dominio string

const (
	DominioDatos      Dominio = "datos"
	DominioDocumentos Dominio = "documentos"
)

// Sufijos de descripción para el contrato de discernimiento. El router es el
// sesgo determinista; la descripción es la red de seguridad que SIEMPRE lee el
// LLM (si el router fallara o devolviera indefinido, la frontera de dominio
// sigue escrita en el contrato).
const (
	discernimientoDatosSufijo      = " NO uses esta tool para procedimientos, protocolos o recomendaciones: esa información vive en los documentos (buscar_documentos)."
	discernimientoDocumentosSufijo = " NO uses esta tool para datos de lotes, rendimientos, aplicaciones o solicitudes: esos datos viven en la DB (consultar_lotes, consultar_rendimientos, etc.)."
)

// Result es lo que una tool devuelve. Data es un valor estructurado que el
// orquestador le pasa al LLM para que arme la respuesta con números reales.
type Result struct {
	Data any `json:"data"`
}

// Tool es un contrato de herramienta.
type Tool struct {
	Name         string
	Description  string
	Dominio      Dominio
	ParamsSchema map[string]any // JSON Schema, para el tool calling del LLM
	Run          func(ctx context.Context, raw json.RawMessage) (Result, error)
}

// Def es la definición tipada de una tool, lo que consume el orquestador
// para mandarle el contrato al LLM.
type Def struct {
	Name        string
	Description string
	Dominio     Dominio
	Parameters  map[string]any // JSON Schema
}

// Registry mantiene las tools disponibles en orden estable.
type Registry struct {
	byName map[string]Tool
	order  []string
}

func NewRegistry(tools ...Tool) *Registry {
	r := &Registry{byName: make(map[string]Tool, len(tools))}
	for _, t := range tools {
		r.byName[t.Name] = t
		r.order = append(r.order, t.Name)
	}
	return r
}

func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.byName[name]
	return t, ok
}

func (r *Registry) Names() []string { return r.order }

// Defs devuelve las definiciones tipadas en orden estable.
func (r *Registry) Defs() []Def {
	out := make([]Def, 0, len(r.order))
	for _, name := range r.order {
		t := r.byName[name]
		out = append(out, Def{Name: t.Name, Description: t.Description, Dominio: t.Dominio, Parameters: t.ParamsSchema})
	}
	return out
}

// Schemas devuelve las tools en el formato de tool calling del LLM
// (OpenAI / Gemini compatibles).
func (r *Registry) Schemas() []map[string]any {
	defs := r.Defs()
	out := make([]map[string]any, 0, len(defs))
	for _, d := range defs {
		out = append(out, map[string]any{
			"name":        d.Name,
			"description": d.Description,
			"parameters":  d.Parameters,
		})
	}
	return out
}