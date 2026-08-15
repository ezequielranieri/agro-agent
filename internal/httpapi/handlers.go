package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/agro-agent/agro-agent/internal/agent"
	"github.com/agro-agent/agro-agent/internal/llm"
	"github.com/agro-agent/agro-agent/internal/tenant"
)

// chatRequest es el contrato del POST /api/v1/chat. `message` es obligatorio;
// `history` es opcional (puede faltar o ser []). Los nombres en snake_case
// son los que usa el cliente HTTP.
type chatRequest struct {
	Message string        `json:"message"`
	History []chatHistory `json:"history"`
}

// chatHistory es un turno previo de la conversación. Solo se aceptan los
// roles que el orquestador sabe reenviar (user/assistant); un rol "tool" en
// el historial del cliente se rechaza (fail-closed, el cliente no fabrica
// resultados de tools).
type chatHistory struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

// chatResponse es la respuesta JSON del chat (no aplica a SSE).
type chatResponse struct {
	Reply      string   `json:"reply"`
	ToolTrace  []string `json:"tool_trace"`
	Iterations int      `json:"iterations"`
	Usage      usageOut `json:"usage"`
	ElapsedMs  int64    `json:"elapsed_ms"`
}

type usageOut struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// acceptsSSE negocia el contenido: si el cliente pide text/event-stream
// entregamos streaming; si no, JSON (el default). Nunca devolver SSE a un
// cliente que no lo pidió.
func acceptsSSE(r *http.Request) bool {
	for _, v := range r.Header.Values("Accept") {
		for _, part := range strings.Split(v, ",") {
			if strings.TrimSpace(part) == "text/event-stream" {
				return true
			}
		}
	}
	return false
}

// handleChat es el endpoint principal. Flujo: valida el payload → inyecta el
// OnEvent por request → corre el orquestador → serializa JSON o SSE.
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	// El ctx ya trae el tenant (lo inyectó requireAuth antes de llegar acá).
	req, err := decodeChatRequest(r.Body)
	if err != nil {
		// Payload inválido → 400 uniforme; el detalle queda en el log.
		s.log.Debug("chat: request inválido", "err", err)
		writeJSONErr(w, http.StatusBadRequest, "invalid request")
		return
	}

	history := toLLMMessages(req.History)
	sse := acceptsSSE(r)
	// OnEvent por request: el agente compartido no se toca (concurrencia).
	// El closure captura `w` y escribe SSE SOLO si el cliente lo pidió; en
	// modo JSON el trace sale de Answer.ToolCalls, no de los eventos.
	perReq := agent.New(s.agent.Provider(), s.agent.Registry(), agent.Options{
		MaxIterations: s.agent.MaxIterations(),
		OnEvent: func(e agent.Event) {
			if sse {
				writeSSEEvent(w, e)
			}
		},
	})

	if sse {
		sseHeaders(w)
		// Flush el encabezado antes de correr: el cliente ve el stream abierto
		// apenas empieza la primera llamada al LLM (que puede tardar).
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}

	answer, err := perReq.Run(r.Context(), history, req.Message)
	if err != nil {
		// El error real NO viaja al cliente (uniforme): se loguea acá.
		s.log.Error("chat: el agente falló", "err", err, "tenant", tenantIDFromCtx(r))
		if sse {
			writeSSEString(w, map[string]any{"type": "error", "error": "agent failed"})
			return
		}
		writeJSONErr(w, http.StatusInternalServerError, "agent failed")
		return
	}

	if sse {
		// El evento done es el último y cierra la respuesta.
		writeSSEString(w, map[string]any{
			"type": "done", "reply": answer.Text, "iterations": answer.Iterations,
		})
		return
	}

	// Header de contenido antes del status: si el JSON falla, WriteHeader
	// todavía no se llamó y el statusWriter conserva el default 200.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(chatResponse{
		Reply:      answer.Text,
		ToolTrace:  answer.ToolCalls,
		Iterations: answer.Iterations,
		Usage: usageOut{
			PromptTokens:     answer.Usage.PromptTokens,
			CompletionTokens: answer.Usage.CompletionTokens,
		},
		ElapsedMs: answer.Elapsed.Milliseconds(),
	})
}

// decodeChatRequest deserializa el body con DisallowUnknownFields: un campo
// fuera del contrato es un error (fail-closed, regla del proyecto).
func decodeChatRequest(r io.Reader) (chatRequest, error) {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	var req chatRequest
	if err := dec.Decode(&req); err != nil {
		return chatRequest{}, err
	}
	if strings.TrimSpace(req.Message) == "" {
		return chatRequest{}, errors.New("message vacío")
	}
	for _, h := range req.History {
		if h.Role != "user" && h.Role != "assistant" {
			return chatRequest{}, errors.New("rol de historial inválido")
		}
		if strings.TrimSpace(h.Text) == "" {
			return chatRequest{}, errors.New("texto de historial vacío")
		}
	}
	return req, nil
}

// toLLMMessages mapea el historial validado al modelo del puerto llm.
func toLLMMessages(history []chatHistory) []llm.Message {
	out := make([]llm.Message, 0, len(history))
	for _, h := range history {
		out = append(out, llm.Message{Role: llm.Role(h.Role), Text: h.Text})
	}
	return out
}

// sseHeaders prepara el stream. Sin cache: cada request es una sesión nueva
// del agente y el cliente no debe reutilizar una respuesta vieja.
func sseHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
}

// writeSSEEvent serializa un evento del orquestador a un frame SSE.
func writeSSEEvent(w http.ResponseWriter, e agent.Event) {
	payload := map[string]any{"type": e.Type, "tool": e.Tool}
	if e.Type == "tool_call_result" {
		payload["ok"] = e.OK
	}
	writeSSEString(w, payload)
}

// writeSSEString emite un frame data: y lo flushea de inmediato: si el
// orquestador tarda, el cliente ya ve el progreso en vivo.
func writeSSEString(w http.ResponseWriter, payload map[string]any) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	// Frame SSE: cada evento termina en una línea en blanco (doble \n).
	w.Write([]byte("data: " + string(raw) + "\n\n"))
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// tenantIDFromCtx escribe el tenant en el log de error sin romper si el
// contexto no lo tuviera (defensa: el log es un dato, no el flujo).
func tenantIDFromCtx(r *http.Request) string {
	if tid, err := tenant.FromContext(r.Context()); err == nil {
		return strconv.FormatInt(int64(tid), 10)
	}
	return "?"
}
