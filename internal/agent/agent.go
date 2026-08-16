// Package agent es el ORQUESTADOR: el loop de tool calling que convierte la
// capa de tools en un agente. Depende solo de puertos:
//   - llm.Provider (Gemini en prod, fake en tests)
//   - tools.Registry (los contratos)
//
// Reglas del loop:
//   - El TenantID viaja en el ctx y llega a las tools; el orquestador no lo toca.
//   - El error de una tool vuelve al LLM como resultado (puede explicar o
//     reintentar). La guarda de maxIterations corta los loops infinitos.
//   - El usage se acumula en cada iteración (alimenta costo por consulta).
package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/agro-agent/agro-agent/internal/llm"
	"github.com/agro-agent/agro-agent/internal/router"
	"github.com/agro-agent/agro-agent/internal/tools"
)

// Options configura el orquestador.
type Options struct {
	MaxIterations int           // default 5
	ChatTimeout   time.Duration // tope por llamada al LLM; <= 0 usa 60s
	OnEvent       func(Event)   // nil-safe: si es nil, no se notifica nada
	// Router clasifica la consulta por dominio (datos/documentos) para
	// exponer al LLM solo las tools relevantes. Nil = comportamiento clásico:
	// todas las tools, el modelo decide solo por descripción.
	Router router.Clasificador
}

// Event es un punto de observación del loop de tool calling. Lo consume el
// transporte HTTP para el streaming SSE y el audit: el orquestador solo lo
// emite, nunca decide qué hacer con él.
type Event struct {
	Type string // "tool_call_started" | "tool_call_result"
	Tool string // nombre de la tool
	OK   bool   // solo en "tool_call_result": si la tool terminó sin error
}

// Agent ejecuta el loop de tool calling.
type Agent struct {
	provider      llm.Provider
	registry      *tools.Registry
	router        router.Clasificador
	maxIterations int
	chatTimeout   time.Duration
	onEvent       func(Event)
}

func New(provider llm.Provider, registry *tools.Registry, opts Options) *Agent {
	if opts.MaxIterations <= 0 {
		opts.MaxIterations = 5
	}
	// defaultChatTimeout acota cada llamada al LLM: un provider colgado no
	// puede pinchar la goroutine del request para siempre (el SSE moriría sin
	// el evento done).
	const defaultChatTimeout = 60 * time.Second
	if opts.ChatTimeout <= 0 {
		opts.ChatTimeout = defaultChatTimeout
	}
	return &Agent{provider: provider, registry: registry, router: opts.Router, maxIterations: opts.MaxIterations, chatTimeout: opts.ChatTimeout, onEvent: opts.OnEvent}
}

// onEventCb emite un evento si hay callback configurado. Separado en un método
// para que el loop llame un solo punto y nunca se paniquee por un nil.
func (a *Agent) onEventCb(e Event) {
	if a.onEvent != nil {
		a.onEvent(e)
	}
}

// Provider expone el puerto LLM. Lo usa el transporte para construir un agente
// por request (con su propio OnEvent) sin compartir estado mutable entre
// requests concurrentes.
func (a *Agent) Provider() llm.Provider { return a.provider }

// Registry expone el registro de tools (ver Provider).
func (a *Agent) Registry() *tools.Registry { return a.registry }

// MaxIterations expone el límite de iteraciones (ver Provider).
func (a *Agent) MaxIterations() int { return a.maxIterations }

// Router expone el clasificador de dominios (ver Provider). El transporte lo
// propaga a sus agentes efímeros por request (mismo patrón que Registry).
func (a *Agent) Router() router.Clasificador { return a.router }

// Answer es el resultado final del loop.
type Answer struct {
	Text       string   // respuesta final del LLM
	ToolCalls  []string // trace de tools invocadas en orden
	Iterations int
	Usage      llm.Usage
	Elapsed    time.Duration
}

// toLLMTools mapea las definiciones del registro (tools) al tipo del puerto
// (llm). Mantiene los paquetes desacoplados.
func toLLMTools(defs []tools.Def) []llm.ToolSchema {
	out := make([]llm.ToolSchema, 0, len(defs))
	for _, d := range defs {
		out = append(out, llm.ToolSchema{Name: d.Name, Description: d.Description, Parameters: d.Parameters})
	}
	return out
}

// filterByDominio conserva solo las definiciones cuyo Dominio está en la lista
// que decidió el router. Mantiene el orden estable del registro.
func filterByDominio(defs []tools.Def, dominios []tools.Dominio) []tools.Def {
	set := make(map[tools.Dominio]struct{}, len(dominios))
	for _, d := range dominios {
		set[d] = struct{}{}
	}
	out := make([]tools.Def, 0, len(defs))
	for _, def := range defs {
		if _, ok := set[def.Dominio]; ok {
			out = append(out, def)
		}
	}
	return out
}

// Run ejecuta el agente sobre un historial previo y un mensaje del usuario.
// El ctx DEBE traer el TenantID (lo inyecta el middleware de auth).
func (a *Agent) Run(ctx context.Context, history []llm.Message, userText string) (Answer, error) {
	messages := make([]llm.Message, 0, len(history)+4)
	messages = append(messages, history...)
	messages = append(messages, llm.Message{Role: llm.RoleUser, Text: userText})

	// Discernimiento: se clasifica UNA vez (sobre la pregunta más reciente,
	// no sobre el historial completo) y se filtra el contrato que recibe el
	// LLM. Sesgo no barrera: error del router o clasificación indefinida ⇒
	// exponer todo. El registro conserva todas las tools: la resolución por
	// nombre (a.registry.Get) sigue funcionando aunque el contrato esté filtrado.
	defs := a.registry.Defs()
	if a.router != nil {
		dominios, err := a.router.Clasificar(ctx, userText)
		if err != nil {
			// El router nunca debe romper la conversación: ante error, sesgo
			// no barrera ⇒ se exponen todas las tools.
			dominios = nil
		}
		if len(dominios) > 0 {
			defs = filterByDominio(defs, dominios)
		}
	}

	start := time.Now()
	answer := Answer{}
	var total llm.Usage

	for i := 0; i < a.maxIterations; i++ {
		// Deadline POR ITERACIÓN: el timeout deriva del ctx del request, así
		// que la cancelación del cliente (SSE cerrado, shutdown) corta antes
		// del tope. Sin esto, un provider colgado pincha la goroutine y el
		// request nunca termina.
		iterCtx, cancel := context.WithTimeout(ctx, a.chatTimeout)
		resp, err := a.provider.Chat(iterCtx, messages, toLLMTools(defs))
		cancel()
		if err != nil {
			return Answer{}, fmt.Errorf("agent: llamada al LLM (iteración %d): %w", i+1, err)
		}
		// El request murió mientras el LLM respondía: no seguimos gastando
		// llamadas al modelo (costo + latencia) para nadie que ya no escucha.
		if ctx.Err() != nil {
			return Answer{}, fmt.Errorf("agent: contexto cancelado (iteración %d): %w", i+1, ctx.Err())
		}
		total = total.Add(resp.Usage)

		if len(resp.ToolCalls) == 0 {
			// Respuesta final.
			return Answer{
				Text:       resp.Text,
				ToolCalls:  answer.ToolCalls,
				Iterations: i + 1,
				Usage:      total,
				Elapsed:    time.Since(start),
			}, nil
		}

		// El mensaje del asistente con sus tool calls va primero (Gemini exige
		// el functionCall en rol model antes de los functionResponse).
		messages = append(messages, llm.Message{Role: llm.RoleAssistant, ToolCalls: resp.ToolCalls})
		for _, call := range resp.ToolCalls {
			answer.ToolCalls = append(answer.ToolCalls, call.Name)

			tool, ok := a.registry.Get(call.Name)
			if !ok {
				// Tool desconocida: se lo contamos al LLM, que debería corregir.
				messages = append(messages, llm.Message{
					Role: llm.RoleTool, ToolName: call.Name, ToolID: call.ID,
					ToolResult: map[string]any{"error": fmt.Sprintf("tool desconocida: %s", call.Name)},
				})
				continue
			}

			// Evento ANTES de ejecutar: el transporte lo transmite en vivo al
			// cliente (SSE). El error de la tool igual emite su evento: la
			// diferencia la cuenta el campo OK.
			a.onEventCb(Event{Type: "tool_call_started", Tool: call.Name})
			result, err := tool.Run(ctx, call.Args)
			a.onEventCb(Event{Type: "tool_call_result", Tool: call.Name, OK: err == nil})
			if err != nil {
				// Error de la tool → al LLM como resultado (fail-recoverable).
				// El tenant ausente también llega así: no se filtra dato alguno.
				messages = append(messages, llm.Message{
					Role: llm.RoleTool, ToolName: call.Name, ToolID: call.ID,
					ToolResult: map[string]any{"error": err.Error()},
				})
				continue
			}
			messages = append(messages, llm.Message{
				Role: llm.RoleTool, ToolName: call.Name, ToolID: call.ID,
				ToolResult: result.Data,
			})
		}
	}

	return Answer{}, fmt.Errorf("agent: se alcanzó el máximo de %d iteraciones sin respuesta final (posible loop)", a.maxIterations)
}
