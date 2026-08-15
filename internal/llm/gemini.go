package llm

import (
	"context"
	"encoding/json"
	"fmt"

	"google.golang.org/genai"
)

// Gemini es el adapter del puerto Provider para Google Gemini (API pública,
// incluye el free tier). El orquestador no sabe que existe: habla con
// Provider.
type Gemini struct {
	client *genai.Client
	model  string
}

// NewGemini crea el adapter. apiKey vacía no falla acá (el SDK la lee de
// GEMINI_API_KEY / GOOGLE_API_KEY); model vacío usa gemini-2.0-flash.
func NewGemini(ctx context.Context, apiKey, model string) (*Gemini, error) {
	client, err := genai.NewClient(ctx, &genai.ClientConfig{APIKey: apiKey})
	if err != nil {
		return nil, fmt.Errorf("llm: crear cliente Gemini: %w", err)
	}
	if model == "" {
		model = "gemini-3.6-flash"
	}
	return &Gemini{client: client, model: model}, nil
}

// systemPrompt orienta al modelo: datos reales de tools, nunca inventar.
// Es la instrucción de usuario del agente (idioma del contexto: español).
const systemPrompt = "Sos un agente agropecuario que asiste a cooperativas y productores. " +
	"Respondé SIEMPRE usando los datos reales que devuelven las herramientas. " +
	"Nunca inventes números, lotes ni fechas. Si una herramienta devuelve vacío o un error, " +
	"decilo con claridad y sin adornos. Respondé en el idioma de la pregunta, de forma concisa y directa."

func (g *Gemini) Chat(ctx context.Context, messages []Message, tools []ToolSchema) (Response, error) {
	contents, err := toGeminiContents(messages)
	if err != nil {
		return Response{}, err
	}

	config := &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{
			Parts: []*genai.Part{genai.NewPartFromText(systemPrompt)},
		},
		Tools:       toGeminiTools(tools),
		Temperature: ptr(float32(0.2)),
	}

	resp, err := g.client.Models.GenerateContent(ctx, g.model, contents, config)
	if err != nil {
		return Response{}, fmt.Errorf("llm: Gemini: %w", err)
	}
	if resp == nil {
		return Response{}, nil
	}

	out := Response{}
	// Extraemos el texto SIN el helper resp.Text() (que loguea un warning
	// cuando hay FunctionCall): armamos el texto solo si no hay tools.
	if len(resp.Candidates) > 0 && resp.Candidates[0].Content != nil {
		for _, part := range resp.Candidates[0].Content.Parts {
			if part.FunctionCall == nil && part.Text != "" {
				out.Text += part.Text
			}
		}
	}
	// Iteramos las parts (no el helper FunctionCalls()) porque la
	// thought_signature vive en la Part y se pierde con el helper.
	if len(resp.Candidates) > 0 && resp.Candidates[0].Content != nil {
		for _, part := range resp.Candidates[0].Content.Parts {
			if part.FunctionCall == nil {
				continue
			}
			fc := part.FunctionCall
			raw, err := json.Marshal(fc.Args)
			if err != nil {
				return Response{}, fmt.Errorf("llm: serializar args de %q: %w", fc.Name, err)
			}
			out.ToolCalls = append(out.ToolCalls, ToolCall{
				ID:               fc.ID,
				Name:             fc.Name,
				Args:             raw,
				ThoughtSignature: part.ThoughtSignature,
			})
		}
	}
	if um := resp.UsageMetadata; um != nil {
		out.Usage = Usage{
			PromptTokens:     int(um.PromptTokenCount),
			CompletionTokens: int(um.CandidatesTokenCount),
		}
	}
	return out, nil
}

// toGeminiContents convierte el modelo de mensajes del puerto al formato de
// Gemini. La danza de roles es obligatoria: functionCall va en rol "model",
// functionResponse en rol "user" (Gemini no tiene rol "tool").
func toGeminiContents(messages []Message) ([]*genai.Content, error) {
	contents := make([]*genai.Content, 0, len(messages))
	for _, m := range messages {
		var parts []*genai.Part
		switch m.Role {
		case RoleUser:
			if m.Text != "" {
				parts = append(parts, genai.NewPartFromText(m.Text))
			}
			contents = append(contents, &genai.Content{Parts: parts, Role: "user"})
		case RoleTool:
			// El resultado de una tool va como functionResponse en rol "user"
			// (Gemini no tiene rol "tool").
			parts = append(parts, genai.NewPartFromFunctionResponse(m.ToolName, map[string]any{"result": m.ToolResult}))
			contents = append(contents, &genai.Content{Parts: parts, Role: "user"})
		case RoleAssistant:
			if m.Text != "" {
				parts = append(parts, genai.NewPartFromText(m.Text))
			}
			for _, tc := range m.ToolCalls {
				var args map[string]any
				if err := json.Unmarshal(tc.Args, &args); err != nil {
					return nil, fmt.Errorf("llm: args inválidos en tool call %q: %w", tc.Name, err)
				}
				part := genai.NewPartFromFunctionCall(tc.Name, args)
				// La thought_signature de la respuesta del modelo es
				// OBLIGATORIA al reenviar el functionCall.
				part.ThoughtSignature = tc.ThoughtSignature
				parts = append(parts, part)
			}
			contents = append(contents, &genai.Content{Parts: parts, Role: "model"})
		default:
			return nil, fmt.Errorf("llm: rol %q no soportado por Gemini", m.Role)
		}
	}
	return contents, nil
}

// toGeminiTools convierte los schemas del registro al formato de Gemini.
func toGeminiTools(tools []ToolSchema) []*genai.Tool {
	if len(tools) == 0 {
		return nil
	}
	decls := make([]*genai.FunctionDeclaration, 0, len(tools))
	for _, t := range tools {
		decls = append(decls, &genai.FunctionDeclaration{
			Name:                 t.Name,
			Description:          t.Description,
			ParametersJsonSchema: t.Parameters,
		})
	}
	return []*genai.Tool{{FunctionDeclarations: decls}}
}

func ptr[T any](v T) *T { return &v }