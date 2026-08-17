package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// groqEndpoint es la base de la API compatible-OpenAI de Groq.
const groqEndpoint = "https://api.groq.com/openai/v1/chat/completions"

// DefaultGroqModel es el modelo por defecto del adapter Groq. Verificado en
// vivo: llama-3.3-70b-versatile responde y hace tool calling en el free tier.
const DefaultGroqModel = "llama-3.3-70b-versatile"

// Groq es el adapter del puerto Provider para Groq (API compatible OpenAI).
// El orquestador no sabe que existe: habla con Provider. Sin reintento
// interno: un error transitorio (429/5xx/red) se propaga para que el
// FallbackProvider decida el failover.
type Groq struct {
	apiKey   string
	model    string
	endpoint string // default groqEndpoint; los tests lo apuntan al httptest
	client   *http.Client
}

// NewGroq crea el adapter. model vacío usa DefaultGroqModel. La key es
// obligatoria: viaja como Authorization Bearer en cada request.
func NewGroq(apiKey, model string) *Groq {
	if model == "" {
		model = DefaultGroqModel
	}
	return &Groq{apiKey: apiKey, model: model, endpoint: groqEndpoint, client: &http.Client{}}
}

// Chat envía la conversación a Groq y devuelve la respuesta del puerto.
func (g *Groq) Chat(ctx context.Context, messages []Message, tools []ToolSchema) (Response, error) {
	body, err := g.buildBody(messages, tools)
	if err != nil {
		return Response{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.endpoint, bytes.NewReader(body))
	if err != nil {
		return Response{}, fmt.Errorf("llm: Groq: crear request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+g.apiKey)

	resp, err := g.client.Do(req)
	if err != nil {
		// Error de transporte (red/timeout): se propaga tal cual; IsTransient
		// lo clasifica como transitorio vía net.Error.
		return Response{}, fmt.Errorf("llm: Groq: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Body acotado a 4KB: no volcamos basura del proveedor en los logs.
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return Response{}, &groqError{Status: resp.StatusCode, Body: string(b)}
	}

	var oai oaiResponse
	if err := json.NewDecoder(resp.Body).Decode(&oai); err != nil {
		return Response{}, fmt.Errorf("llm: Groq: decodificar respuesta: %w", err)
	}
	return parseGroqResponse(&oai), nil
}

// --- Wire format OpenAI/Groq ------------------------------------------------

type oaiMessage struct {
	Role       string        `json:"role"`
	Content    *string       `json:"content,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
	ToolCalls  []oaiToolCall `json:"tool_calls,omitempty"`
}

type oaiToolCall struct {
	ID       string              `json:"id"`
	Type     string              `json:"type"`
	Function oaiToolCallFunction `json:"function"`
}

type oaiToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // string JSON con los args (formato OpenAI)
}

type oaiTool struct {
	Type     string        `json:"type"`
	Function oaiToolSchema `json:"function"`
}

type oaiToolSchema struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type oaiRequest struct {
	Model       string       `json:"model"`
	Messages    []oaiMessage `json:"messages"`
	Tools       []oaiTool    `json:"tools,omitempty"`
	Temperature float32      `json:"temperature"`
}

type oaiChoice struct {
	Message struct {
		Content   string        `json:"content"`
		ToolCalls []oaiToolCall `json:"tool_calls"`
	} `json:"message"`
}

type oaiResponse struct {
	Choices []oaiChoice `json:"choices"`
	Usage   struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

// buildBody arma el JSON del request. El system prompt viaja como primer
// mensaje de rol "system": mismo texto que Gemini inyecta en
// SystemInstruction, así el comportamiento de tool calling es equivalente
// entre proveedores.
func (g *Groq) buildBody(messages []Message, tools []ToolSchema) ([]byte, error) {
	oaiMsgs := make([]oaiMessage, 0, len(messages)+1)
	sp := systemPrompt
	oaiMsgs = append(oaiMsgs, oaiMessage{Role: "system", Content: &sp})
	for _, m := range messages {
		om, err := toOAIMessage(m)
		if err != nil {
			return nil, err
		}
		oaiMsgs = append(oaiMsgs, om)
	}
	req := oaiRequest{
		Model:       g.model,
		Messages:    oaiMsgs,
		Temperature: 0.2, // misma temperatura que el adapter Gemini
	}
	if len(tools) > 0 {
		req.Tools = toOAITools(tools)
	}
	b, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("llm: Groq: serializar request: %w", err)
	}
	return b, nil
}

// toOAIMessage mapea el modelo de mensajes del puerto al wire de OpenAI/Groq:
// user → "user", assistant → "assistant" (con tool_calls), tool → "tool"
// (resultado con tool_call_id).
func toOAIMessage(m Message) (oaiMessage, error) {
	switch m.Role {
	case RoleUser:
		text := m.Text
		return oaiMessage{Role: "user", Content: &text}, nil
	case RoleAssistant:
		var content *string
		if m.Text != "" {
			content = &m.Text
		}
		var tcs []oaiToolCall
		for _, tc := range m.ToolCalls {
			// ThoughtSignature es específica de Gemini: Groq no la usa y se
			// descarta (el puerto la tolera nil).
			tcs = append(tcs, oaiToolCall{
				ID:       tc.ID,
				Type:     "function",
				Function: oaiToolCallFunction{Name: tc.Name, Arguments: string(tc.Args)},
			})
		}
		return oaiMessage{Role: "assistant", Content: content, ToolCalls: tcs}, nil
	case RoleTool:
		content, err := stringifyToolResult(m.ToolResult)
		if err != nil {
			return oaiMessage{}, err
		}
		return oaiMessage{Role: "tool", Content: &content, ToolCallID: m.ToolID}, nil
	default:
		return oaiMessage{}, fmt.Errorf("llm: rol %q no soportado por Groq", m.Role)
	}
}

// stringifyToolResult serializa el resultado de una tool al wire de Groq.
// Espeja la convención de gemini.go: el resultado viaja envuelto en
// {"result": ...} para que el modelo reciba la misma forma que con Gemini.
func stringifyToolResult(r any) (string, error) {
	b, err := json.Marshal(map[string]any{"result": r})
	if err != nil {
		return "", fmt.Errorf("llm: Groq: serializar resultado de tool: %w", err)
	}
	return string(b), nil
}

// toOAITools convierte los schemas del registro al formato "tools" de OpenAI.
func toOAITools(tools []ToolSchema) []oaiTool {
	out := make([]oaiTool, 0, len(tools))
	for _, t := range tools {
		out = append(out, oaiTool{
			Type: "function",
			Function: oaiToolSchema{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		})
	}
	return out
}

// parseGroqResponse convierte la respuesta wire de Groq al tipo del puerto.
// Los ToolCalls llegan con ThoughtSignature nil: es un campo específico de
// Gemini y el orquestador lo copia sin paniquear.
func parseGroqResponse(oai *oaiResponse) Response {
	out := Response{}
	if len(oai.Choices) > 0 {
		msg := oai.Choices[0].Message
		out.Text = msg.Content
		for _, tc := range msg.ToolCalls {
			out.ToolCalls = append(out.ToolCalls, ToolCall{
				ID:   tc.ID,
				Name: tc.Function.Name,
				Args: json.RawMessage(tc.Function.Arguments),
			})
		}
	}
	out.Usage = Usage{
		PromptTokens:     oai.Usage.PromptTokens,
		CompletionTokens: oai.Usage.CompletionTokens,
	}
	return out
}

// groqError es un error tipado de la API de Groq: lleva el código HTTP para
// que IsTransient clasifique. El body llega acotado (4KB).
type groqError struct {
	Status int
	Body   string
}

func (e *groqError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("Groq: HTTP %d", e.Status)
	}
	return fmt.Sprintf("Groq: HTTP %d: %s", e.Status, e.Body)
}

// transient clasifica el código HTTP del error: solo 429 (cuota/rate limit) y
// 5xx (fallo del proveedor) son transitorios. Un 4xx es un error de contrato
// y reintentarlo o hacer failover no lo arregla.
func (e *groqError) transient() bool {
	return transientStatus(e.Status)
}

// transientStatus determina si un código HTTP del proveedor es transitorio:
// 429 (cuota/rate limit) y 5xx (fallo del proveedor). Cualquier otro código
// NO lo es (los 4xx son errores de contrato).
func transientStatus(code int) bool {
	switch code {
	case http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	}
	return false
}
