package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"google.golang.org/genai"
)

// newGroqTestServer arma un httptest.Server que captura el body del request
// (para auditar el wire format) y responde con respondWith. Devuelve un Groq
// apuntado al server de test.
func newGroqTestServer(t *testing.T, respondWith string, capture *map[string]any) *Groq {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q, esperaba %q", got, "Bearer test-key")
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, esperaba application/json", ct)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("request inválido: %v", err)
		}
		*capture = body
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, respondWith)
	}))
	t.Cleanup(srv.Close)
	return &Groq{apiKey: "test-key", model: "llama-test", endpoint: srv.URL, client: srv.Client()}
}

// messagesAsMap devuelve el array "messages" capturado del request.
func messagesAsMap(t *testing.T, capture map[string]any) []map[string]any {
	t.Helper()
	raw, ok := capture["messages"].([]any)
	if !ok {
		t.Fatalf("request sin campo messages")
	}
	out := make([]map[string]any, 0, len(raw))
	for _, m := range raw {
		mm, ok := m.(map[string]any)
		if !ok {
			t.Fatalf("mensaje no es objeto: %#v", m)
		}
		out = append(out, mm)
	}
	return out
}

// TestGroqChat_TextoPlano: respuesta de texto + usage, y el wire format
// (system prompt primero, temperature 0.2, sin tools).
func TestGroqChat_TextoPlano(t *testing.T) {
	var captured map[string]any
	g := newGroqTestServer(t, `{"choices":[{"message":{"content":"Hola"}}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`, &captured)

	resp, err := g.Chat(context.Background(), []Message{{Role: RoleUser, Text: "hola"}}, nil)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Text != "Hola" {
		t.Errorf("Text = %q, esperaba %q", resp.Text, "Hola")
	}
	if len(resp.ToolCalls) != 0 {
		t.Errorf("ToolCalls = %v, esperaba vacío", resp.ToolCalls)
	}
	if resp.Usage.PromptTokens != 10 || resp.Usage.CompletionTokens != 5 {
		t.Errorf("Usage = %+v, esperaba 10/5", resp.Usage)
	}

	if got := captured["model"]; got != "llama-test" {
		t.Errorf("model = %v, esperaba llama-test", got)
	}
	if got := captured["temperature"].(float64); got != 0.2 {
		t.Errorf("temperature = %v, esperaba 0.2", got)
	}
	if _, ok := captured["tools"]; ok {
		t.Errorf("no debe enviar tools cuando no hay")
	}
	msgs := messagesAsMap(t, captured)
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, esperaba 2 (system + user)", len(msgs))
	}
	if msgs[0]["role"] != "system" || msgs[0]["content"] != systemPrompt {
		t.Errorf("system: rol=%v content=%v, esperaba el systemPrompt del agente", msgs[0]["role"], msgs[0]["content"])
	}
	if msgs[1]["role"] != "user" || msgs[1]["content"] != "hola" {
		t.Errorf("user: rol=%v content=%v", msgs[1]["role"], msgs[1]["content"])
	}
}

// TestGroqChat_RespuestaConToolCalls: parsing de tool_calls (ID/Name/Args
// round-trip) y envío del array tools en el request.
func TestGroqChat_RespuestaConToolCalls(t *testing.T) {
	var captured map[string]any
	raw := `{"choices":[{"message":{"content":null,"tool_calls":[{"id":"call_abc","type":"function","function":{"name":"consultar_lotes","arguments":"{\"campania\":\"2024\"}"}}]}}],"usage":{"prompt_tokens":22,"completion_tokens":7}}`
	g := newGroqTestServer(t, raw, &captured)

	tools := []ToolSchema{{
		Name:        "consultar_lotes",
		Description: "Lista lotes de una campaña",
		Parameters:  map[string]any{"type": "object"},
	}}
	resp, err := g.Chat(context.Background(), []Message{{Role: RoleUser, Text: "lotes 2024"}}, tools)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Text != "" {
		t.Errorf("Text = %q, esperaba vacío cuando hay tool_calls", resp.Text)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("ToolCalls = %d, esperaba 1", len(resp.ToolCalls))
	}
	tc := resp.ToolCalls[0]
	if tc.ID != "call_abc" {
		t.Errorf("ID = %q, esperaba call_abc", tc.ID)
	}
	if tc.Name != "consultar_lotes" {
		t.Errorf("Name = %q, esperaba consultar_lotes", tc.Name)
	}
	if string(tc.Args) != `{"campania":"2024"}` {
		t.Errorf("Args = %q, esperaba el JSON crudo de arguments", string(tc.Args))
	}
	if tc.ThoughtSignature != nil {
		t.Errorf("ThoughtSignature debe ser nil en Groq, obtuve %v", tc.ThoughtSignature)
	}
	if resp.Usage.PromptTokens != 22 || resp.Usage.CompletionTokens != 7 {
		t.Errorf("Usage = %+v, esperaba 22/7", resp.Usage)
	}

	// Wire: el array tools debe tener el formato {"type":"function","function":{...}}.
	toolsOut := captured["tools"].([]any)
	if len(toolsOut) != 1 {
		t.Fatalf("tools = %d, esperaba 1", len(toolsOut))
	}
	tw := toolsOut[0].(map[string]any)
	if tw["type"] != "function" {
		t.Errorf("tools[0].type = %v, esperaba function", tw["type"])
	}
	fn := tw["function"].(map[string]any)
	if fn["name"] != "consultar_lotes" || fn["description"] != "Lista lotes de una campaña" {
		t.Errorf("function = %v, esperaba name+description", fn)
	}
	if fn["parameters"].(map[string]any)["type"] != "object" {
		t.Errorf("parameters.type = %v, esperaba object", fn["parameters"])
	}
}

// TestGroqChat_ConversacionToolCalling: mapeo assistant(tool_calls) → tool
// (resultado) en el wire format, y que la ThoughtSignature de Gemini no viaja.
func TestGroqChat_ConversacionToolCalling(t *testing.T) {
	var captured map[string]any
	g := newGroqTestServer(t, `{"choices":[{"message":{"content":"Encontré 1 lote."}}],"usage":{}}`, &captured)

	msgs := []Message{
		{Role: RoleUser, Text: "¿lotes de 2024?"},
		{
			Role: RoleAssistant,
			ToolCalls: []ToolCall{{
				ID:               "call_1",
				Name:             "consultar_lotes",
				Args:             json.RawMessage(`{"campania":"2024"}`),
				ThoughtSignature: []byte("gemini-sig"),
			}},
		},
		{
			Role: RoleTool, ToolName: "consultar_lotes", ToolID: "call_1",
			ToolResult: map[string]any{"lotes": []map[string]any{{"id": 4, "campania": "2024"}}},
		},
	}
	if _, err := g.Chat(context.Background(), msgs, nil); err != nil {
		t.Fatalf("Chat: %v", err)
	}

	msgsOut := messagesAsMap(t, captured)
	if len(msgsOut) != 4 {
		t.Fatalf("messages = %d, esperaba 4 (system+user+assistant+tool)", len(msgsOut))
	}

	asst := msgsOut[2]
	if asst["role"] != "assistant" {
		t.Errorf("role = %v, esperaba assistant", asst["role"])
	}
	if _, ok := asst["content"]; ok {
		t.Errorf("assistant con solo tool_calls no debe llevar content")
	}
	if _, ok := asst["thought_signature"]; ok {
		t.Errorf("la ThoughtSignature de Gemini no debe viajar al wire de Groq")
	}
	tcs := asst["tool_calls"].([]any)
	if len(tcs) != 1 {
		t.Fatalf("tool_calls = %d, esperaba 1", len(tcs))
	}
	tc := tcs[0].(map[string]any)
	if tc["id"] != "call_1" || tc["type"] != "function" {
		t.Errorf("tool_call = %v, esperaba id call_1 type function", tc)
	}
	fn := tc["function"].(map[string]any)
	if fn["name"] != "consultar_lotes" {
		t.Errorf("function.name = %v, esperaba consultar_lotes", fn["name"])
	}
	if fn["arguments"] != `{"campania":"2024"}` {
		t.Errorf("function.arguments = %v, esperaba el JSON crudo", fn["arguments"])
	}

	tl := msgsOut[3]
	if tl["role"] != "tool" || tl["tool_call_id"] != "call_1" {
		t.Errorf("tool msg = %v, esperaba role tool + tool_call_id call_1", tl)
	}
	content, ok := tl["content"].(string)
	if !ok {
		t.Fatalf("tool content no es string: %#v", tl["content"])
	}
	if strings.Contains(content, "gemini-sig") {
		t.Errorf("la ThoughtSignature se filtró al resultado de la tool")
	}
	var inner map[string]any
	if err := json.Unmarshal([]byte(content), &inner); err != nil {
		t.Fatalf("tool content no es JSON válido: %v", err)
	}
	// Misma convención que gemini.go: el resultado viaja envuelto en {"result":...}.
	if _, hasResult := inner["result"]; !hasResult {
		t.Errorf("tool content = %s, esperaba el envelope {\"result\": ...} de gemini.go", content)
	}
}

// TestGroqChat_ErrorNo2xx: el adapter devuelve un groqError tipado con el
// código HTTP, y IsTransient lo clasifica (429/5xx transitorios, 4xx no).
func TestGroqChat_ErrorNo2xx(t *testing.T) {
	cases := []struct {
		name          string
		status        int
		body          string
		wantTransient bool
	}{
		{"429 rate limit", http.StatusTooManyRequests, `{"error":"rate limit"}`, true},
		{"500 proveedor caído", http.StatusInternalServerError, `{"error":"boom"}`, true},
		{"503 no disponible", http.StatusServiceUnavailable, `{"error":"overloaded"}`, true},
		{"400 contrato roto", http.StatusBadRequest, `{"error":"bad request"}`, false},
		{"404 inexistente", http.StatusNotFound, `not found`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(c.status)
				fmt.Fprint(w, c.body)
			}))
			defer srv.Close()
			g := &Groq{apiKey: "k", model: "m", endpoint: srv.URL, client: srv.Client()}

			_, err := g.Chat(context.Background(), []Message{{Role: RoleUser, Text: "x"}}, nil)
			if err == nil {
				t.Fatal("esperaba error")
			}
			var ge *groqError
			if !errors.As(err, &ge) {
				t.Fatalf("esperaba *groqError, obtuve %T", err)
			}
			if ge.Status != c.status {
				t.Errorf("Status = %d, esperaba %d", ge.Status, c.status)
			}
			if got := IsTransient(err); got != c.wantTransient {
				t.Errorf("IsTransient = %v, esperaba %v", got, c.wantTransient)
			}
		})
	}
}

// TestGroqChat_ErrorDeRed: un error de transporte (server caído) se propaga y
// se clasifica como transitorio.
func TestGroqChat_ErrorDeRed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // cierra antes del request → connection refused

	g := &Groq{apiKey: "k", model: "m", endpoint: url, client: srv.Client()}
	_, err := g.Chat(context.Background(), []Message{{Role: RoleUser, Text: "x"}}, nil)
	if err == nil {
		t.Fatal("esperaba error de red")
	}
	if !IsTransient(err) {
		t.Errorf("error de red debe ser transitorio, obtuve %v", err)
	}
}

// TestIsTransient: clasificación de los tres casos (Groq, Gemini, red) y de
// los errores que NO ameritan failover.
func TestIsTransient(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"groq 429", &groqError{Status: http.StatusTooManyRequests}, true},
		{"groq 500", &groqError{Status: http.StatusInternalServerError}, true},
		{"groq 502", &groqError{Status: http.StatusBadGateway}, true},
		{"groq 503", &groqError{Status: http.StatusServiceUnavailable}, true},
		{"groq 504", &groqError{Status: http.StatusGatewayTimeout}, true},
		{"groq 400", &groqError{Status: http.StatusBadRequest}, false},
		{"groq 404", &groqError{Status: http.StatusNotFound}, false},
		{"gemini 429", genai.APIError{Code: http.StatusTooManyRequests}, true},
		{"gemini 500", genai.APIError{Code: http.StatusInternalServerError}, true},
		{"gemini 400", genai.APIError{Code: http.StatusBadRequest}, false},
		{"gemini envuelto", fmt.Errorf("llm: Gemini: %w", genai.APIError{Code: http.StatusTooManyRequests}), true},
		{"red dns", &net.DNSError{Err: "no such host"}, true},
		{"red url.Error", &url.Error{Op: "Get", URL: "https://x", Err: &net.DNSError{Err: "no such host"}}, true},
		{"error genérico", errors.New("boom"), false},
		{"nil", nil, false},
	}
	for _, c := range cases {
		if got := IsTransient(c.err); got != c.want {
			t.Errorf("%s: IsTransient = %v, esperaba %v", c.name, got, c.want)
		}
	}
}

// TestGroqChat_RespuestaSinChoices: respuesta vacía no pánico, usage en cero.
func TestGroqChat_RespuestaSinChoices(t *testing.T) {
	var captured map[string]any
	g := newGroqTestServer(t, `{"choices":[],"usage":{"prompt_tokens":1,"completion_tokens":0}}`, &captured)
	resp, err := g.Chat(context.Background(), []Message{{Role: RoleUser, Text: "x"}}, nil)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Text != "" || len(resp.ToolCalls) != 0 {
		t.Errorf("respuesta vacía esperada, obtuve %+v", resp)
	}
	if resp.Usage.PromptTokens != 1 {
		t.Errorf("Usage = %+v", resp.Usage)
	}
}
