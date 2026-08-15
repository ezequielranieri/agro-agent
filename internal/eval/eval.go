// Package eval es el harness de evaluación del agente: un golden set de casos
// (pregunta → tools esperadas → verificaciones de la respuesta) que corre
// contra el agente REAL (Gemini + Postgres) y reporta PASS/FAIL.
//
// Es una herramienta de medición, no una suite de unit tests: el LLM es
// no-determinista, así que los resultados se interpretan por tendencia (qué
// % de casos eligió la tool correcta), no como un gate binario.
package eval

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/agro-agent/agro-agent/internal/agent"
	"github.com/agro-agent/agro-agent/internal/domain"
	"github.com/agro-agent/agro-agent/internal/identity"
	"github.com/agro-agent/agro-agent/internal/tenant"
)

// Case es un caso del golden set. La semántica de los verificadores:
//
//	ExpectedTools    tools que deben aparecer EN ORDEN (subsecuencia) en el trace.
//	MustContain      substrings que deben estar en la respuesta final.
//	MustContainAny   al menos UNA de estas substrings debe estar (frases
//	                 equivalentes: "no tengo datos" / "no hay información").
//	MustNotContain   substrings que NO deben estar (detecta alucinaciones).
//	Writes           true = el caso escribe en la DB (HITL): se saltea en el
//	                 modo default para que las corridas sean read-only.
type Case struct {
	ID             string
	Description    string
	Question       string
	ExpectedTools  []string
	MustContain    []string
	MustContainAny []string
	MustNotContain []string
	Writes         bool
}

// Result es el outcome de un caso.
type Result struct {
	Case       Case
	Pass       bool
	ToolCalls  []string
	Iterations int
	Usage      struct {
		PromptTokens     int
		CompletionTokens int
	}
	Elapsed time.Duration
	// Failures acumula los motivos de FAIL (tool ausente, subcadena faltante,
	// subcadena prohibida). Vacío = PASS.
	Failures []string
}

// Run ejecuta todos los casos contra el agente con el tenant dado. skipWrites
// excluye los casos que escriben (default true: las corridas de eval son
// read-only y no contaminan la DB con solicitudes HITL).
func Run(ctx context.Context, ag *agent.Agent, cases []Case, tenantID domain.TenantID, actorID, actorRole string, skipWrites bool) []Result {
	out := make([]Result, 0, len(cases))
	for _, c := range cases {
		if skipWrites && c.Writes {
			continue
		}
		out = append(out, runOne(ctx, ag, c, tenantID, actorID, actorRole))
	}
	return out
}

func runOne(ctx context.Context, ag *agent.Agent, c Case, tenantID domain.TenantID, actorID, actorRole string) Result {
	res := Result{Case: c}

	// El tenant y el actor viajan en el ctx (igual que el middleware de auth).
	runCtx := tenant.WithID(ctx, tenantID)
	runCtx = identity.WithUserRole(runCtx, actorID, actorRole)

	answer, err := ag.Run(runCtx, nil, c.Question)
	res.ToolCalls = answer.ToolCalls
	res.Iterations = answer.Iterations
	res.Usage.PromptTokens = answer.Usage.PromptTokens
	res.Usage.CompletionTokens = answer.Usage.CompletionTokens
	res.Elapsed = answer.Elapsed

	if err != nil {
		res.Failures = append(res.Failures, fmt.Sprintf("error: %v", err))
		res.Pass = false
		return res
	}

	// 1) Tools esperadas: subsecuencia en orden. Si el agente consulta datos
	// antes de decidir (exploración), las tools esperadas deben aparecer en
	// ese orden dentro del trace completo.
	checkExpectedTools(&res, c.ExpectedTools, answer.ToolCalls)

	// 2) Subcadenas obligatorias.
	for _, want := range c.MustContain {
		if !strings.Contains(strings.ToLower(answer.Text), strings.ToLower(want)) {
			res.Failures = append(res.Failures, fmt.Sprintf("respuesta sin %q", want))
		}
	}
	// 2b) Al menos una de las frases equivalentes.
	if len(c.MustContainAny) > 0 {
		anyOK := false
		for _, want := range c.MustContainAny {
			if strings.Contains(strings.ToLower(answer.Text), strings.ToLower(want)) {
				anyOK = true
				break
			}
		}
		if !anyOK {
			res.Failures = append(res.Failures, fmt.Sprintf("respuesta sin ninguna de %v", c.MustContainAny))
		}
	}
	// 3) Subcadenas prohibidas (anti-alucinación).
	for _, banned := range c.MustNotContain {
		if strings.Contains(strings.ToLower(answer.Text), strings.ToLower(banned)) {
			res.Failures = append(res.Failures, fmt.Sprintf("respuesta contiene %q (prohibido)", banned))
		}
	}

	res.Pass = len(res.Failures) == 0
	return res
}

// checkExpectedTools verifica que expected aparezca como subsecuencia (en
// orden) dentro del trace de tools. La subsecuencia permite al agente
// explorar (tools extra) sin romper el caso: importa la tool decisiva.
func checkExpectedTools(res *Result, expected, actual []string) {
	idx := 0
	for _, call := range actual {
		if idx < len(expected) && call == expected[idx] {
			idx++
		}
	}
	if idx < len(expected) {
		res.Failures = append(res.Failures,
			fmt.Sprintf("tools esperadas %v no aparecieron en orden (trace: %v)", expected, actual))
	}
}

// Summary agrega los resultados por caso y métricas globales.
type Summary struct {
	Total       int
	Passed      int
	Failed      int
	ToolAcc     float64 // % de casos con la tool esperada presente (sobre pasados+fallidos)
	AvgElapsed  time.Duration
	TotalTokens int
}

func Summarize(results []Result) Summary {
	s := Summary{Total: len(results)}
	var elapsed time.Duration
	for _, r := range results {
		elapsed += r.Elapsed
		s.TotalTokens += r.Usage.PromptTokens + r.Usage.CompletionTokens
		if r.Pass {
			s.Passed++
		} else {
			s.Failed++
		}
		// Tool accuracy: el caso pasó la verificación de tools aunque falle
		// por contenido (medida de routing, la más valiosa del RAG).
		toolOK := true
		for _, f := range r.Failures {
			if strings.HasPrefix(f, "tools esperadas") {
				toolOK = false
				break
			}
		}
		if toolOK && len(r.Case.ExpectedTools) > 0 {
			s.ToolAcc++
		}
	}
	if s.Total > 0 {
		s.AvgElapsed = elapsed / time.Duration(s.Total)
		s.ToolAcc = s.ToolAcc / float64(s.Total) * 100
	}
	return s
}