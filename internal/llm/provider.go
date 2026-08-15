// Package llm define el PUERTO del proveedor de LLM y sus tipos de mensaje.
// El orquestador (internal/agent) depende de esta interfaz, nunca de un
// proveedor concreto: Gemini es un adapter más, y los tests usan un fake.
package llm

import (
	"context"
	"encoding/json"
)

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// ToolCall es una invocación de herramienta pedida por el modelo.
type ToolCall struct {
	ID   string          // correlación con el resultado de la tool
	Name string          // nombre de la tool (del registro)
	Args json.RawMessage // argumentos JSON en bruto, los valida la tool

	// ThoughtSignature: requerida por la API Gemini al reenviar un
	// functionCall. Se copia de la respuesta y se devuelve intacta.
	ThoughtSignature []byte
}

// Message es un turno de la conversación enviado al LLM.
type Message struct {
	Role      Role
	Text      string
	ToolCalls []ToolCall // para mensajes del asistente
	ToolName  string     // para mensajes de tipo tool (resultado)
	ToolID    string
	ToolResult any // resultado estructurado de la tool
}

// ToolSchema es el contrato de una tool que el LLM puede invocar.
type ToolSchema struct {
	Name        string
	Description string
	Parameters  map[string]any // JSON Schema
}

// Usage contabiliza tokens de una llamada (alimenta costo por consulta).
type Usage struct {
	PromptTokens     int
	CompletionTokens int
}

// Response es la respuesta del LLM: texto final, tools a ejecutar, o ambos.
type Response struct {
	Text      string
	ToolCalls []ToolCall
	Usage     Usage
}

// Provider es el puerto: conversación + tools → respuesta + uso.
type Provider interface {
	Chat(ctx context.Context, messages []Message, tools []ToolSchema) (Response, error)
}

// Total suma dos usos (para acumular en el loop del orquestador).
func (u Usage) Add(o Usage) Usage {
	return Usage{PromptTokens: u.PromptTokens + o.PromptTokens, CompletionTokens: u.CompletionTokens + o.CompletionTokens}
}