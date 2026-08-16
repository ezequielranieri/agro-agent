// Command api es el backend HTTP de agro-agent: recibe requests autenticados
// (Bearer JWT emitido por agro-iam), aísla cada uno en su tenant y corre el
// orquestador sobre Postgres real + Gemini.
//
// Variables de entorno:
//
//	AGRO_DATABASE_URL  DSN de Postgres (default local del dev).
//	GEMINI_API_KEY     clave del proveedor LLM (REQUERIDA).
//	JWT_SECRET         secret compartido con agro-iam (REQUERIDA).
//	PORT               puerto de escucha (default 8080).
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/agro-agent/agro-agent/internal/agent"
	"github.com/agro-agent/agro-agent/internal/approval"
	pg2 "github.com/agro-agent/agro-agent/internal/approval/pg"
	"github.com/agro-agent/agro-agent/internal/auth"
	"github.com/agro-agent/agro-agent/internal/embedding"
	"github.com/agro-agent/agro-agent/internal/httpapi"
	"github.com/agro-agent/agro-agent/internal/llm"
	"github.com/agro-agent/agro-agent/internal/router"
	"github.com/agro-agent/agro-agent/internal/store/pg"
	"github.com/agro-agent/agro-agent/internal/tools"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// --- Configuración ------------------------------------------------------
	// GEMINI_API_KEY y JWT_SECRET son REQUERIDAS: arrancar sin ellas es
	// arrancar un servicio que falla en la primera request. Mejor morir claro
	// en el boot que confundir un 500 en producción.
	geminiKey := os.Getenv("GEMINI_API_KEY")
	if geminiKey == "" {
		log.Error("GEMINI_API_KEY es requerida")
		os.Exit(1)
	}
	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		log.Error("JWT_SECRET es requerida")
		os.Exit(1)
	}
	dsn := os.Getenv("AGRO_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://postgres:postgres@localhost:5432/agro"
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// --- Persistencia -------------------------------------------------------
	// El server HTTP atiende cada request en su propia goroutine y el frontend
	// dispara lotes/approvals/aplicaciones en paralelo. Una única *pgx.Conn
	// compartida NO es segura bajo ese paralelismo (choca con "conn busy" y
	// devuelve 500). El pool reparte las llamadas entre varias conexiones.
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Error("conectar a Postgres", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	// --- LLM + orquestador --------------------------------------------------
	gemini, err := llm.NewGemini(ctx, geminiKey, "gemini-3.6-flash")
	if err != nil {
		log.Error("crear proveedor Gemini", "err", err)
		os.Exit(1)
	}
	// Embeddings del RAG: mismo proveedor, modelo de vectores propio.
	geminiEmbed, err := embedding.NewGemini(ctx, geminiKey, "")
	if err != nil {
		log.Error("crear proveedor de embeddings", "err", err)
		os.Exit(1)
	}

	// --- Registro de tools (mismo wiring que cmd/demo) ----------------------
	// Todos los stores comparten el MISMO *pgxpool.Pool: el pool es thread-safe
	// y entrega una conexión distinta por llamada concurrente.
	appsStore := pg.NewAplicacionStore(pool)
	// El store de lotes se comparte entre la tool consultar_lotes y el
	// endpoint GET /api/v1/lotes: un solo pool, un solo estado.
	loteStore := pg.NewLoteStore(pool)
	// HITL: el service de aprobaciones une el store de solicitudes, los
	// resolvers de lote/producto/campaña, el writer de aplicaciones y el
	// auditor. TTL de 24h: la solicitud muere sola si nadie la aprueba.
	approvalSvc := approval.New(
		pg2.NewApprovalStore(pool),
		pg2.NewResolver(pool),
		pg2.NewApplicationWriter(pool),
		pg2.NewAuditor(pool),
		24*time.Hour,
	)
	reg := tools.NewRegistry(
		tools.ConsultarAplicaciones(appsStore),
		tools.ConsultarLotes(loteStore),
		tools.ConsultarRendimientos(pg.NewRendimientoStore(pool)),
		tools.ResumirAplicaciones(appsStore, time.Now),
		tools.DetectarRetrasos(appsStore, time.Now),
		tools.ProgramarAplicacion(approvalSvc),
		tools.ConsultarAprobaciones(approvalSvc),
		tools.BuscarDocumentos(pg.NewDocumentoStore(pool), geminiEmbed),
	)

	ag := agent.New(gemini, reg, agent.Options{MaxIterations: 5, Router: router.NewReglasClasificador()})

	// --- HTTP ----------------------------------------------------------------
	verifier, err := auth.NewVerifier(jwtSecret)
	if err != nil {
		log.Error("configurar auth", "err", err)
		os.Exit(1)
	}
	srv := httpapi.New(ag, verifier, approvalSvc, loteStore, appsStore)

	httpServer := &http.Server{
		Addr:    ":" + port,
		Handler: srv.Handler(),
	}

	log.Info("agro-agent arrancando", "port", port, "tools", reg.Names())

	// Servimos en una goroutine: el main se queda esperando la señal de
	// cierre (SIGINT/SIGTERM) para hacer graceful shutdown.
	errCh := make(chan error, 1)
	go func() {
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		log.Error("servidor HTTP", "err", err)
		os.Exit(1)
	case <-ctx.Done():
		log.Info("señal de cierre recibida, apagando...")
	}

	// Graceful shutdown: esperamos hasta 10s las requests en vuelo y luego
	// cerramos Postgres. El orden importa: primero HTTP, después la DB.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Error("shutdown HTTP", "err", err)
	}
}
