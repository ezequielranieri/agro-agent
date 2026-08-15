package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/agro-agent/agro-agent/internal/domain"
	"github.com/agro-agent/agro-agent/internal/llm"
	"github.com/agro-agent/agro-agent/internal/store"
	"github.com/agro-agent/agro-agent/internal/tenant"
	"github.com/agro-agent/agro-agent/internal/tools"
)

// fakeProvider reproduce un LLM con respuestas GUIONADAS y deterministas.
// Además guarda los mensajes de cada llamada para poder inspeccionarlos.
type fakeProvider struct {
	responses []llm.Response
	callIndex int
	calls     [][]llm.Message
}

func (f *fakeProvider) Chat(_ context.Context, messages []llm.Message, _ []llm.ToolSchema) (llm.Response, error) {
	if f.callIndex >= len(f.responses) {
		return llm.Response{}, errors.New("fake: sin respuestas guionadas")
	}
	r := f.responses[f.callIndex]
	f.callIndex++
	f.calls = append(f.calls, messages)
	return r, nil
}

func callTool(name string, args map[string]any) llm.Response {
	raw, _ := json.Marshal(args)
	return llm.Response{ToolCalls: []llm.ToolCall{{ID: "call-1", Name: name, Args: raw}}}
}

var fixedNow = func() time.Time { return time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC) }

func testRegistry() *tools.Registry {
	apps := []domain.Aplicacion{
		{ID: 1, TenantID: 1, LoteCodigo: "12", CampanaNombre: "2026/2027", Estado: "planificada",
			Producto: "2,4-D Amina", ProductoTipo: "herbicida",
			FechaPlanificada: ptrTime("2026-08-05"), Notas: "RETRASO"},
		{ID: 2, TenantID: 1, LoteCodigo: "4", CampanaNombre: "2026/2027", Estado: "planificada",
			Producto: "2,4-D Amina", ProductoTipo: "herbicida",
			FechaPlanificada: ptrTime("2026-08-08"), Notas: "RETRASO"},
		// Lote 12 del tenant 2, planificada el 01-08 (13 días de retraso si
		// se filtrara mal). NO debe aparecer jamás.
		{ID: 3, TenantID: 2, LoteCodigo: "12", CampanaNombre: "2026/2027", Estado: "planificada",
			Producto: "2,4-D Amina", ProductoTipo: "herbicida",
			FechaPlanificada: ptrTime("2026-08-01")},
	}
	reg := tools.NewRegistry(tools.DetectarRetrasos(&fakeAplicacionStore{apps: apps}, fixedNow))
	return reg
}

func ptrTime(s string) *time.Time {
	t, _ := time.Parse("2006-01-02", s)
	return &t
}

// TestRun_EjecutaToolYDevuelveRespuestaFinal: el flujo feliz del loop.
func TestRun_EjecutaToolYDevuelveRespuestaFinal(t *testing.T) {
	provider := &fakeProvider{responses: []llm.Response{
		callTool("detectar_retrasos", map[string]any{}),
		{Text: "Sí: lote 12 (9 días) y lote 4 (6 días).", Usage: llm.Usage{PromptTokens: 300, CompletionTokens: 20}},
	}}
	a := New(provider, testRegistry(), Options{})
	ctx := tenant.WithID(context.Background(), domain.TenantID(1))

	answer, err := a.Run(ctx, nil, "¿Hay lotes con retraso?")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if answer.Text != "Sí: lote 12 (9 días) y lote 4 (6 días)." {
		t.Errorf("texto inesperado: %q", answer.Text)
	}
	if len(answer.ToolCalls) != 1 || answer.ToolCalls[0] != "detectar_retrasos" {
		t.Errorf("trace de tools incorrecto: %v", answer.ToolCalls)
	}
	if answer.Iterations != 2 {
		t.Errorf("iteraciones esperadas: 2, obtuve %d", answer.Iterations)
	}
	if answer.Usage.PromptTokens != 300 || answer.Usage.CompletionTokens != 20 {
		t.Errorf("usage no acumulado: %+v", answer.Usage)
	}
}

// TestRun_ElTenantDelContextoLlegaALaTool: el resultado de la tool NO puede
// contener datos del tenant 2, aunque el mismo lote 12 exista allá.
func TestRun_ElTenantDelContextoLlegaALaTool(t *testing.T) {
	provider := &fakeProvider{responses: []llm.Response{
		callTool("detectar_retrasos", map[string]any{}),
		{Text: "respuesta final"},
	}}
	a := New(provider, testRegistry(), Options{})
	ctx := tenant.WithID(context.Background(), domain.TenantID(1))

	if _, err := a.Run(ctx, nil, "¿Hay retrasos?"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// La segunda llamada al LLM recibe el resultado de la tool. Debe incluir
	// los retrasos del tenant 1 (lotes 12 y 4) y NUNCA el del tenant 2
	// (dias_retraso: 13).
	second := provider.calls[1]
	var toolMsg *llm.Message
	for i := range second {
		if second[i].Role == llm.RoleTool {
			toolMsg = &second[i]
		}
	}
	if toolMsg == nil {
		t.Fatal("el resultado de la tool no llegó al LLM")
	}
	raw, _ := json.Marshal(toolMsg.ToolResult)
	if strings.Contains(string(raw), `"dias_retraso":13`) {
		t.Fatalf("FUGA DE TENANT: el retraso del tenant 2 llegó al LLM: %s", raw)
	}
	if !strings.Contains(string(raw), `"lote_codigo":"12"`) || !strings.Contains(string(raw), `"lote_codigo":"4"`) {
		t.Errorf("faltan los retrasos del tenant 1: %s", raw)
	}
}

// TestRun_MaxIteracionesCortaElLoop: el LLM pide tools infinitamente → error.
func TestRun_MaxIteracionesCortaElLoop(t *testing.T) {
	provider := &fakeProvider{responses: []llm.Response{
		callTool("detectar_retrasos", map[string]any{}),
		callTool("detectar_retrasos", map[string]any{}),
		callTool("detectar_retrasos", map[string]any{}),
		callTool("detectar_retrasos", map[string]any{}),
		callTool("detectar_retrasos", map[string]any{}),
	}}
	a := New(provider, testRegistry(), Options{MaxIterations: 5})
	ctx := tenant.WithID(context.Background(), domain.TenantID(1))

	_, err := a.Run(ctx, nil, "loop infinito por favor")
	if err == nil || !strings.Contains(err.Error(), "máximo de 5 iteraciones") {
		t.Fatalf("esperaba error por max iteraciones, obtuve: %v", err)
	}
}

// TestRun_ErrorDeToolVuelveAlLLM: si una tool falla, el error es un resultado
// más para el modelo — el agente puede explicarlo en lugar de morir.
func TestRun_ErrorDeToolVuelveAlLLM(t *testing.T) {
	// Registry con una tool que SIEMPRE falla.
	boom := tools.Tool{
		Name: "tool_rota", Description: "falla",
		ParamsSchema: map[string]any{"type": "object"},
		Run: func(_ context.Context, _ json.RawMessage) (tools.Result, error) {
			return tools.Result{}, errors.New("boom: falló la tool")
		},
	}
	provider := &fakeProvider{responses: []llm.Response{
		callTool("tool_rota", map[string]any{}),
		{Text: "Perdón, la herramienta falló: boom: falló la tool"},
	}}
	a := New(provider, tools.NewRegistry(boom), Options{})
	ctx := tenant.WithID(context.Background(), domain.TenantID(1))

	answer, err := a.Run(ctx, nil, "usá la tool rota")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(answer.Text, "falló la tool") {
		t.Errorf("el LLM no recibió el error de la tool: %q", answer.Text)
	}

	second := provider.calls[1]
	raw, _ := json.Marshal(second[len(second)-1].ToolResult)
	if !strings.Contains(string(raw), "boom: falló la tool") {
		t.Errorf("el error no viajó como tool result: %s", raw)
	}
}

// TestRun_HistorialPrevioSeConserva: el historial llega al LLM intacto.
func TestRun_HistorialPrevioSeConserva(t *testing.T) {
	provider := &fakeProvider{responses: []llm.Response{{Text: "hola"}}}
	a := New(provider, testRegistry(), Options{})
	ctx := tenant.WithID(context.Background(), domain.TenantID(1))

	history := []llm.Message{
		{Role: llm.RoleUser, Text: "¿qué campañas hay?"},
		{Role: llm.RoleAssistant, Text: "Hay 2024/2025 y 2026/2027."},
	}
	if _, err := a.Run(ctx, history, "¿y lotes?"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	first := provider.calls[0]
	if len(first) != 3 {
		t.Fatalf("el historial no se conservó: %d mensajes", len(first))
	}
	if first[0].Text != "¿qué campañas hay?" || first[2].Text != "¿y lotes?" {
		t.Errorf("mensajes incorrectos: %+v", first)
	}
}

// fakeAplicacionStore: el mismo fake de los tests de tools (aislamiento real
// del puerto con datos de dos tenants).
type fakeAplicacionStore struct {
	apps []domain.Aplicacion
}

func (f *fakeAplicacionStore) ListAplicaciones(_ context.Context, tid domain.TenantID, flt store.AplicacionFilters) ([]domain.Aplicacion, error) {
	var out []domain.Aplicacion
	for _, a := range f.apps {
		if a.TenantID != tid {
			continue
		}
		if flt.Estado != "" && a.Estado != flt.Estado {
			continue
		}
		if flt.Hasta != nil && (a.FechaPlanificada == nil || a.FechaPlanificada.After(*flt.Hasta)) {
			continue
		}
		out = append(out, a)
	}
	return out, nil
}