package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/agro-agent/agro-agent/internal/agent"
	"github.com/agro-agent/agro-agent/internal/auth"
	"github.com/agro-agent/agro-agent/internal/domain"
	"github.com/agro-agent/agro-agent/internal/httpapi"
	"github.com/agro-agent/agro-agent/internal/llm"
	"github.com/agro-agent/agro-agent/internal/tenant"
	"github.com/agro-agent/agro-agent/internal/tools"
)

// signTestToken firma un JWT con el MISMO formato de agro-iam
// (sub/tenant_id/role/iat/exp). La versión HS512 se usa para probar que el
// verifier rechaza métodos de firma distintos.
func signTestToken(t *testing.T, secret, tenantID, role string) string {
	t.Helper()
	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":       "42",
		"tenant_id": tenantID,
		"role":      role,
		"iat":       now.Unix(),
		"exp":       now.Add(15 * time.Minute).Unix(),
	})
	s, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("firmar token: %v", err)
	}
	return s
}

func signHS512Token(t *testing.T, secret string) string {
	t.Helper()
	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodHS512, jwt.MapClaims{
		"sub": "42", "tenant_id": "1",
		"iat": now.Unix(), "exp": now.Add(15 * time.Minute).Unix(),
	})
	s, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("firmar token: %v", err)
	}
	return s
}

// captureProvider es un fake del puerto llm: captura el ctx y los mensajes
// de la primera llamada para poder inspeccionar el aislamiento de tenant.
type captureProvider struct {
	called   int
	gotCtx   context.Context
	messages []llm.Message
}

func (f *captureProvider) Chat(ctx context.Context, messages []llm.Message, _ []llm.ToolSchema) (llm.Response, error) {
	f.called++
	if f.called == 1 {
		f.gotCtx = ctx
		f.messages = messages
	}
	return llm.Response{Text: "ok", Usage: llm.Usage{PromptTokens: 10, CompletionTokens: 5}}, nil
}

// sseProvider simula un LLM que primero pide una tool y después responde:
// el loop del orquestador ejecuta la tool del registry entre ambas llamadas.
type sseProvider struct{ calls int }

func (f *sseProvider) Chat(_ context.Context, _ []llm.Message, _ []llm.ToolSchema) (llm.Response, error) {
	f.calls++
	if f.calls == 1 {
		raw, _ := json.Marshal(map[string]any{})
		return llm.Response{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "test_tool", Args: raw}}}, nil
	}
	return llm.Response{Text: "listo"}, nil
}

// newTestServer arma el server con un secret fijo y devuelve el handler listo
// para httptest. approvals va en nil: los tests de chat no lo necesitan y el
// server responde 501 en los endpoints HITL (contrato del diseño).
func newTestServer(ag *agent.Agent, secret string) http.Handler {
	verifier, err := auth.NewVerifier(secret)
	if err != nil {
		panic(err)
	}
	return httpapi.New(ag, verifier, nil, &fakeLoteStore{}).Handler()
}

func doChat(t *testing.T, h http.Handler, token, body, accept string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat", strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestChatJSON(t *testing.T) {
	fake := &captureProvider{}
	ag := agent.New(fake, tools.NewRegistry(), agent.Options{MaxIterations: 5})
	h := newTestServer(ag, "secret")

	token := signTestToken(t, "secret", "1", "admin")
	w := doChat(t, h, token, `{"message":"hola"}`, "")

	if w.Code != http.StatusOK {
		t.Fatalf("status esperado 200, obtuve %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Reply      string   `json:"reply"`
		ToolTrace  []string `json:"tool_trace"`
		Iterations int      `json:"iterations"`
		Usage      struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
		ElapsedMs int64 `json:"elapsed_ms"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("respuesta no es JSON: %v", err)
	}
	if resp.Reply != "ok" {
		t.Errorf("reply inesperado: %q", resp.Reply)
	}
	if resp.Iterations != 1 || len(resp.ToolTrace) != 0 {
		t.Errorf("metadatos inesperados: %+v", resp)
	}
	if resp.Usage.PromptTokens != 10 || resp.Usage.CompletionTokens != 5 {
		t.Errorf("usage inesperado: %+v", resp.Usage)
	}
	// El orquestador debió llamar al provider con UN mensaje de rol user.
	if fake.called != 1 || len(fake.messages) != 1 || fake.messages[0].Role != llm.RoleUser || fake.messages[0].Text != "hola" {
		t.Errorf("el provider no recibió el mensaje esperado: %d llamadas, %+v", fake.called, fake.messages)
	}
}

func TestHealthz(t *testing.T) {
	h := newTestServer(agent.New(&captureProvider{}, tools.NewRegistry(), agent.Options{}), "secret")
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK || w.Body.String() != "ok" {
		t.Errorf("healthz: status %d body %q", w.Code, w.Body.String())
	}
}

func TestChatUnauthorized(t *testing.T) {
	ag := agent.New(&captureProvider{}, tools.NewRegistry(), agent.Options{})
	h := newTestServer(ag, "secret")
	body := `{"message":"hola"}`

	// Sin header de Authorization.
	if w := doChat(t, h, "", body, ""); w.Code != http.StatusUnauthorized {
		t.Errorf("sin header: esperaba 401, obtuve %d", w.Code)
	}
	// Token firmado con OTRO secret.
	other := signTestToken(t, "otro-secret", "1", "admin")
	if w := doChat(t, h, other, body, ""); w.Code != http.StatusUnauthorized {
		t.Errorf("otro secret: esperaba 401, obtuve %d", w.Code)
	}
	// Token con alg HS512 (misma clave, método distinto).
	hs512 := signHS512Token(t, "secret")
	if w := doChat(t, h, hs512, body, ""); w.Code != http.StatusUnauthorized {
		t.Errorf("HS512: esperaba 401, obtuve %d", w.Code)
	}
}

func TestChatInvalidBody(t *testing.T) {
	ag := agent.New(&captureProvider{}, tools.NewRegistry(), agent.Options{})
	h := newTestServer(ag, "secret")
	token := signTestToken(t, "secret", "1", "admin")

	cases := []struct{ name, body string }{
		{"JSON malformado", `{no-es-json`},
		{"campo desconocido", `{"message":"hola","tenant_id":999}`},
		{"message vacío", `{"message":"  "}`},
		{"rol de historial inválido", `{"message":"hola","history":[{"role":"system","text":"x"}]}`},
		{"texto de historial vacío", `{"message":"hola","history":[{"role":"user","text":" "}]}`},
	}
	for _, c := range cases {
		w := doChat(t, h, token, c.body, "")
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: esperaba 400, obtuve %d (%s)", c.name, w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "invalid request") {
			t.Errorf("%s: error no uniforme: %s", c.name, w.Body.String())
		}
	}
}

// TestChatTenantIsolation: el tenant del claim viaja por el contexto y llega
// al provider — la prueba end-to-end del aislamiento multi-tenant.
func TestChatTenantIsolation(t *testing.T) {
	for _, tc := range []struct {
		tenant string
		want   domain.TenantID
	}{
		{"1", 1}, {"2", 2},
	} {
		// Un fake por tenant: gotCtx se captura en la primera llamada y un
		// mismo fake no debe reutilizarse entre requests aisladas.
		fake := &captureProvider{}
		ag := agent.New(fake, tools.NewRegistry(), agent.Options{})
		h := newTestServer(ag, "secret")

		token := signTestToken(t, "secret", tc.tenant, "admin")
		w := doChat(t, h, token, `{"message":"hola"}`, "")
		if w.Code != http.StatusOK {
			t.Fatalf("tenant %s: status %d", tc.tenant, w.Code)
		}
		tid, err := tenant.FromContext(fake.gotCtx)
		if err != nil {
			t.Fatalf("tenant %s: no hay TenantID en el ctx: %v", tc.tenant, err)
		}
		if tid != tc.want {
			t.Errorf("tenant %s: esperaba %d, obtuve %d", tc.tenant, tc.want, tid)
		}
	}
}

func TestChatSSE(t *testing.T) {
	// Registry con una tool mínima inline: el LLM (fake) pide ejecutarla y el
	// orquestador la corre entre la primera y la segunda llamada.
	tool := tools.Tool{
		Name: "test_tool", Description: "tool de test",
		ParamsSchema: map[string]any{"type": "object"},
		Run: func(_ context.Context, _ json.RawMessage) (tools.Result, error) {
			return tools.Result{Data: map[string]any{"ok": true}}, nil
		},
	}
	ag := agent.New(&sseProvider{}, tools.NewRegistry(tool), agent.Options{MaxIterations: 5})
	h := newTestServer(ag, "secret")

	token := signTestToken(t, "secret", "1", "admin")
	w := doChat(t, h, token, `{"message":"usá la tool"}`, "text/event-stream")

	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type esperado text/event-stream, obtuve %q", ct)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"type":"tool_call_started"`) || !strings.Contains(body, `"tool":"test_tool"`) {
		t.Errorf("falta el evento tool_call_started: %s", body)
	}
	if !strings.Contains(body, `"type":"tool_call_result"`) || !strings.Contains(body, `"ok":true`) {
		t.Errorf("falta el evento tool_call_result: %s", body)
	}
	if !strings.Contains(body, `"type":"done"`) || !strings.Contains(body, `"reply":"listo"`) {
		t.Errorf("falta el evento done: %s", body)
	}
}

func TestChatHistoryOpcional(t *testing.T) {
	fake := &captureProvider{}
	ag := agent.New(fake, tools.NewRegistry(), agent.Options{})
	h := newTestServer(ag, "secret")
	token := signTestToken(t, "secret", "1", "admin")

	w := doChat(t, h, token, `{"message":"hola","history":[{"role":"user","text":"antes"},{"role":"assistant","text":"previo"}]}`, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	// El historial + el mensaje = 3 mensajes hacia el provider.
	if len(fake.messages) != 3 {
		t.Errorf("historial no reenviado: %d mensajes", len(fake.messages))
	}
}
