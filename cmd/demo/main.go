// Command demo cierra el loop completo: Postgres real → tools → orquestador
// → Gemini. Uso:
//
//	GEMINI_API_KEY=... go run ./cmd/demo "¿Hay lotes con retraso en las aplicaciones planificadas?"
//
// El tenant del demo es el 1 (Cooperativa La Esperanza, del seed).
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/agro-agent/agro-agent/internal/agent"
	"github.com/agro-agent/agro-agent/internal/approval"
	pg2 "github.com/agro-agent/agro-agent/internal/approval/pg"
	"github.com/agro-agent/agro-agent/internal/domain"
	"github.com/agro-agent/agro-agent/internal/embedding"
	"github.com/agro-agent/agro-agent/internal/identity"
	"github.com/agro-agent/agro-agent/internal/llm"
	"github.com/agro-agent/agro-agent/internal/router"
	"github.com/agro-agent/agro-agent/internal/store/pg"
	"github.com/agro-agent/agro-agent/internal/tenant"
	"github.com/agro-agent/agro-agent/internal/tools"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "uso: go run ./cmd/demo \"<pregunta>\"")
		os.Exit(1)
	}
	question := os.Args[1]
	ctx := context.Background()

	// --- Persistencia: Postgres real (schema + seed aplicados) ---------------
	dsn := os.Getenv("AGRO_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://postgres:postgres@localhost:5432/agro"
	}
	// Mismo wiring que cmd/api: un pool de conexiones compartido (thread-safe)
	// por todos los stores, no una única *pgx.Conn.
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "conectar a Postgres: %v\n", err)
		os.Exit(1)
	}
	defer pool.Close()

	// --- Registro de tools: el corazón del sistema ---------------------------
	appsStore := pg.NewAplicacionStore(pool)
	// HITL (mismo wiring que cmd/api): la tool de escritura NO inserta directo;
	// crea una solicitud que un admin/agronomo aprueba con su token.
	approvalSvc := approval.New(
		pg2.NewApprovalStore(pool),
		pg2.NewApplier(pool),
		pg2.NewAuditor(pool),
		24*time.Hour,
	)

	// --- LLM: Gemini free tier (la key vive en el entorno, jamás en el repo)
	gemini, err := llm.NewGemini(ctx, os.Getenv("GEMINI_API_KEY"), "gemini-3.6-flash")
	if err != nil {
		fmt.Fprintf(os.Stderr, "crear proveedor Gemini: %v\n", err)
		os.Exit(1)
	}
	// Embeddings del RAG (mismo wiring que cmd/api).
	geminiEmbed, err := embedding.NewGemini(ctx, os.Getenv("GEMINI_API_KEY"), "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "crear proveedor de embeddings: %v\n", err)
		os.Exit(1)
	}
	reg := tools.NewRegistry(
		tools.ConsultarAplicaciones(appsStore),
		tools.ConsultarLotes(pg.NewLoteStore(pool)),
		tools.ConsultarRendimientos(pg.NewRendimientoStore(pool)),
		tools.ResumirAplicaciones(appsStore, time.Now),
		tools.DetectarRetrasos(appsStore, time.Now),
		tools.ProgramarAplicacion(approvalSvc),
		tools.ConsultarAprobaciones(approvalSvc),
		tools.BuscarDocumentos(pg.NewDocumentoStore(pool), geminiEmbed),
	)

	// --- Orquestador ----------------------------------------------------------
	ag := agent.New(gemini, reg, agent.Options{MaxIterations: 5, Router: router.NewReglasClasificador()})

	// El middleware de auth inyecta tenant y actor (identity); sin HTTP, los
	// fijamos acá para que las tools HITL tengan de quién registrar la acción.
	runCtx := tenant.WithID(ctx, domain.TenantID(1))
	runCtx = identity.WithUserRole(runCtx, "2", "agronomo")

	fmt.Printf("🤖 Pregunta: %s\n", question)
	answer, err := ag.Run(runCtx, nil, question)
	if err != nil {
		fmt.Fprintf(os.Stderr, "el agente falló: %v\n", err)
		os.Exit(1)
	}

	// --- Trace: la parte que vende el demo -----------------------------------
	for i, name := range answer.ToolCalls {
		fmt.Printf("   → [tool %d/%d] %s\n", i+1, len(answer.ToolCalls), name)
	}
	fmt.Printf("📋 Respuesta (%d iteraciones · %.2fs · %d tokens in / %d out):\n%s\n",
		answer.Iterations, answer.Elapsed.Seconds(),
		answer.Usage.PromptTokens, answer.Usage.CompletionTokens, answer.Text)
}
