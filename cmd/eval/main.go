// Command eval corre el golden set del agente contra Postgres real + Gemini
// y reporta PASS/FAIL por caso + métricas globales.
//
// Uso:
//
//	GEMINI_API_KEY=... go run ./cmd/eval            # read-only (saltea HITL)
//	GEMINI_API_KEY=... go run ./cmd/eval --writes  # incluye casos que escriben
//
// Variables de entorno:
//
//	AGRO_DATABASE_URL  DSN de Postgres (default local del dev).
//	GEMINI_API_KEY     clave del proveedor (REQUERIDA).
//	PORT               sin uso: este cmd no escucha.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/agro-agent/agro-agent/internal/agent"
	"github.com/agro-agent/agro-agent/internal/approval"
	pg2 "github.com/agro-agent/agro-agent/internal/approval/pg"
	"github.com/agro-agent/agro-agent/internal/domain"
	"github.com/agro-agent/agro-agent/internal/embedding"
	"github.com/agro-agent/agro-agent/internal/eval"
	"github.com/agro-agent/agro-agent/internal/llm"
	"github.com/agro-agent/agro-agent/internal/router"
	"github.com/agro-agent/agro-agent/internal/store/pg"
	"github.com/agro-agent/agro-agent/internal/tools"
)

func main() {
	includeWrites := flag.Bool("writes", false, "incluir casos que escriben (HITL)")
	flag.Parse()

	geminiKey := os.Getenv("GEMINI_API_KEY")
	if geminiKey == "" {
		fmt.Fprintln(os.Stderr, "GEMINI_API_KEY es requerida")
		os.Exit(1)
	}
	dsn := os.Getenv("AGRO_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://postgres:postgres@localhost:5432/agro"
	}

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "conectar a Postgres: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close(ctx)

	// Mismo wiring que cmd/demo + cmd/api.
	appsStore := pg.NewAplicacionStore(conn)
	approvalSvc := approval.New(
		pg2.NewApprovalStore(conn),
		pg2.NewResolver(conn),
		pg2.NewApplicationWriter(conn),
		pg2.NewAuditor(conn),
		24*time.Hour,
	)
	gemini, err := llm.NewGemini(ctx, geminiKey, "gemini-3.6-flash")
	if err != nil {
		fmt.Fprintf(os.Stderr, "crear proveedor Gemini: %v\n", err)
		os.Exit(1)
	}
	geminiEmbed, err := embedding.NewGemini(ctx, geminiKey, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "crear proveedor de embeddings: %v\n", err)
		os.Exit(1)
	}
	reg := tools.NewRegistry(
		tools.ConsultarAplicaciones(appsStore),
		tools.ConsultarLotes(pg.NewLoteStore(conn)),
		tools.ConsultarRendimientos(pg.NewRendimientoStore(conn)),
		tools.ResumirAplicaciones(appsStore, time.Now),
		tools.DetectarRetrasos(appsStore, time.Now),
		tools.ProgramarAplicacion(approvalSvc),
		tools.ConsultarAprobaciones(approvalSvc),
		tools.BuscarDocumentos(pg.NewDocumentoStore(conn), geminiEmbed),
	)
	ag := agent.New(gemini, reg, agent.Options{MaxIterations: 5, Router: router.NewReglasClasificador()})

	// Tenant 1 (Cooperativa La Esperanza) con un agronomo, como en el demo.
	results := eval.Run(ctx, ag, eval.GoldenSet, domain.TenantID(1), "2", "agronomo", !*includeWrites)

	fmt.Println("=== EVAL agro-agent ===")
	for _, r := range results {
		mark := "✅"
		if !r.Pass {
			mark = "❌"
		}
		fmt.Printf("%s [%s] %s\n", mark, r.Case.ID, r.Case.Description)
		fmt.Printf("   tools: %v | iteraciones: %d | %.1fs | %d tok\n",
			r.ToolCalls, r.Iterations, r.Elapsed.Seconds(),
			r.Usage.PromptTokens+r.Usage.CompletionTokens)
		for _, f := range r.Failures {
			fmt.Printf("   ✗ %s\n", f)
		}
	}

	s := eval.Summarize(results)
	fmt.Println("=== RESUMEN ===")
	fmt.Printf("casos: %d | PASS: %d | FAIL: %d | tool accuracy: %.0f%% | promedio: %.1fs | tokens: %d\n",
		s.Total, s.Passed, s.Failed, s.ToolAcc, s.AvgElapsed.Seconds(), s.TotalTokens)
}