package eval

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/agro-agent/agro-agent/internal/agent"
	"github.com/agro-agent/agro-agent/internal/domain"
	"github.com/agro-agent/agro-agent/internal/llm"
	"github.com/agro-agent/agro-agent/internal/tools"
)

// scriptedProvider simula un LLM con un guion: respuestas tool-call seguidas
// de una respuesta final. Permite testear el harness sin Gemini.
type scriptedProvider struct {
	steps []llm.Response
	idx   int
}

func (s *scriptedProvider) Chat(ctx context.Context, messages []llm.Message, tools []llm.ToolSchema) (llm.Response, error) {
	if s.idx >= len(s.steps) {
		return llm.Response{}, nil
	}
	r := s.steps[s.idx]
	s.idx++
	return r, nil
}

func agentWith(steps []llm.Response) *agent.Agent {
	// Registro mínimo con las tools que el guion puede llamar.
	reg := newRegistryForEval()
	return agent.New(&scriptedProvider{steps: steps}, reg, agent.Options{MaxIterations: 5})
}

func TestRunPass(t *testing.T) {
	// El agente llama detectar_retrasos y responde con el retraso real.
	steps := []llm.Response{
		{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "detectar_retrasos", Args: []byte(`{}`)}}},
		{Text: "Hay retrasos en los lotes 4, 7 y 12."},
	}
	ag := agentWith(steps)
	results := Run(context.Background(), ag, []Case{{
		ID:            "retrasos",
		Question:      "¿Hay lotes con retraso?",
		ExpectedTools: []string{"detectar_retrasos"},
		MustContain:   []string{"retraso", "lote"},
	}}, domain.TenantID(1), "2", "agronomo", true)

	if len(results) != 1 || !results[0].Pass {
		t.Fatalf("esperaba PASS, got %+v", results)
	}
}

func TestRunToolOrderSubsequence(t *testing.T) {
	// El agente explora (consultar_aplicaciones) antes de la tool decisiva:
	// la subsecuencia en orden debe aceptar la tool extra.
	steps := []llm.Response{
		{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "consultar_aplicaciones", Args: []byte(`{}`)}}},
		{ToolCalls: []llm.ToolCall{{ID: "c2", Name: "detectar_retrasos", Args: []byte(`{}`)}}},
		{Text: "Hay retrasos en el lote 12."},
	}
	ag := agentWith(steps)
	results := Run(context.Background(), ag, []Case{{
		ID:            "retrasos",
		Question:      "¿Hay lotes con retraso?",
		ExpectedTools: []string{"detectar_retrasos"},
		MustContain:   []string{"retraso"},
	}}, domain.TenantID(1), "2", "agronomo", true)

	if !results[0].Pass {
		t.Fatalf("esperaba PASS con subsecuencia, got %+v", results[0].Failures)
	}
}

func TestRunFailsToolMissing(t *testing.T) {
	steps := []llm.Response{
		{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "consultar_lotes", Args: []byte(`{}`)}}},
		{Text: "No encontré nada."},
	}
	ag := agentWith(steps)
	results := Run(context.Background(), ag, []Case{{
		ID:            "retrasos",
		Question:      "¿Hay lotes con retraso?",
		ExpectedTools: []string{"detectar_retrasos"},
	}}, domain.TenantID(1), "2", "agronomo", true)

	if results[0].Pass {
		t.Fatal("esperaba FAIL: tool esperada ausente")
	}
}

func TestRunAntiHallucination(t *testing.T) {
	// El modelo responde SIN inventar: usa una frase de rechazo válida.
	steps := []llm.Response{
		{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "consultar_rendimientos", Args: []byte(`{}`)}}},
		{Text: "No tengo datos de esa campaña en la base."},
	}
	ag := agentWith(steps)
	results := Run(context.Background(), ag, []Case{{
		ID:             "no-alucina",
		Question:       "¿Qué rindió la soja en 2023/2024?",
		ExpectedTools:  []string{"consultar_rendimientos"},
		MustContainAny: []string{"no tengo datos", "no hay datos"},
		MustNotContain: []string{"3,8", "3.8"},
	}}, domain.TenantID(1), "2", "agronomo", true)

	if !results[0].Pass {
		t.Fatalf("esperaba PASS anti-alucinación, got %+v", results[0].Failures)
	}
}

func TestRunCatchesHallucination(t *testing.T) {
	// El modelo INVENTA un número: el caso debe fallar por la subcadena.
	steps := []llm.Response{
		{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "consultar_rendimientos", Args: []byte(`{}`)}}},
		{Text: "El rendimiento de soja 2023/2024 fue 3,8 tn/ha."},
	}
	ag := agentWith(steps)
	results := Run(context.Background(), ag, []Case{{
		ID:             "no-alucina",
		Question:       "¿Qué rindió la soja en 2023/2024?",
		ExpectedTools:  []string{"consultar_rendimientos"},
		MustContainAny: []string{"no tengo datos"},
		MustNotContain: []string{"3,8"},
	}}, domain.TenantID(1), "2", "agronomo", true)

	if results[0].Pass {
		t.Fatal("esperaba FAIL: el modelo alucinó 3,8")
	}
}

func TestRunSkipsWritesByDefault(t *testing.T) {
	ag := agentWith(nil)
	results := Run(context.Background(), ag, []Case{
		{ID: "lectura", Question: "x"},
		{ID: "escritura", Question: "y", Writes: true},
	}, domain.TenantID(1), "2", "agronomo", true)

	if len(results) != 1 || results[0].Case.ID != "lectura" {
		t.Fatalf("esperaba solo el caso de lectura, got %d casos", len(results))
	}
}

func TestSummarize(t *testing.T) {
	results := []Result{
		{Pass: true, Case: Case{ID: "a", ExpectedTools: []string{"t1"}}},
		{Pass: false, Case: Case{ID: "b", ExpectedTools: []string{"t1"}}, Failures: []string{"tools esperadas [t1] no aparecieron"}},
		{Pass: true, Case: Case{ID: "c", ExpectedTools: []string{"t1"}}, Elapsed: 2 * time.Second, Usage: struct {
			PromptTokens     int
			CompletionTokens int
		}{PromptTokens: 100}},
	}
	s := Summarize(results)
	if s.Total != 3 || s.Passed != 2 || s.Failed != 1 {
		t.Fatalf("conteo incorrecto: %+v", s)
	}
	// Tool accuracy: 2 de 3 casos pasaron la verificación de tools.
	if s.ToolAcc != 66.66666666666666 {
		t.Fatalf("tool accuracy esperada 66.67%%, got %v", s.ToolAcc)
	}
}

// newRegistryForEval arma un registro mínimo con los nombres que usan los
// tests. Las tools devuelven vacío (nunca tocan la DB: el guion del provider
// solo necesita que el registro reconozca el nombre de la tool).
func newRegistryForEval() *tools.Registry {
	dummy := func(ctx context.Context, raw json.RawMessage) (tools.Result, error) {
		return tools.Result{Data: map[string]any{"ok": true}}, nil
	}
	var all []tools.Tool
	for _, name := range []string{
		"detectar_retrasos", "consultar_aplicaciones", "consultar_rendimientos",
		"consultar_lotes", "resumir_aplicaciones", "consultar_aprobaciones",
		"programar_aplicacion", "buscar_documentos",
	} {
		all = append(all, tools.Tool{Name: name, Run: dummy})
	}
	return tools.NewRegistry(all...)
}