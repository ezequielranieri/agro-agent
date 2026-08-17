// Command api es el backend HTTP de agro-agent: recibe requests autenticados
// (Bearer JWT emitido por agro-iam), aísla cada uno en su tenant y corre el
// orquestador sobre Postgres real + un proveedor de LLM (Gemini y/o Groq).
//
// Variables de entorno:
//
//	AGRO_DATABASE_URL  DSN de Postgres (default local del dev).
//	GEMINI_API_KEY     clave de Gemini (chat + embeddings). OPCIONAL si
//	                   GROQ_API_KEY está presente (modo solo-Groq).
//	GROQ_API_KEY       clave de Groq, respaldo del chat ante cuota de Gemini
//	                   agotada / proveedor caído. OPCIONAL.
//	GROQ_MODEL         modelo de Groq (default llama-3.3-70b-versatile).
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
	// JWT_SECRET es REQUERIDA: arrancar sin ella es arrancar un servicio que
	// falla en la primera request. Mejor morir claro en el boot que confundir
	// un 500 en producción. (Las keys de LLM se resuelven más abajo: al menos
	// GEMINI_API_KEY o GROQ_API_KEY debe estar presente.)
	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		log.Error("JWT_SECRET es requerida")
		os.Exit(1)
	}
	if jwtSecret == "change-me" {
		// El valor por defecto del .env.example NO es un secreto: quien emite
		// tokens con él es cualquiera que lea el repo. Mismo chequeo que agro-iam.
		log.Error("JWT_SECRET no debe ser el valor por defecto 'change-me'")
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
	// Compositor de proveedores de chat. GEMINI_API_KEY y GROQ_API_KEY son
	// OPCIONALES individualmente, pero al menos una es REQUERIDA:
	//   - ambas    → FallbackProvider(Gemini → Groq): el chat sobrevive a la
	//                cuota del free tier de Gemini (429) cayendo a Groq.
	//   - solo Gemini → comportamiento histórico, sin cambios.
	//   - solo Groq   → Groq como primario (el RAG queda sin embeddings).
	//   - ninguna     → fatal: arrancar sin LLM es arrancar un servicio roto.
	geminiKey := os.Getenv("GEMINI_API_KEY")
	groqKey := os.Getenv("GROQ_API_KEY")

	var chatProvider llm.Provider
	var llmDesc string
	switch {
	case geminiKey == "" && groqKey == "":
		log.Error("se requiere al menos una key de LLM: GEMINI_API_KEY y/o GROQ_API_KEY")
		os.Exit(1)

	case geminiKey != "" && groqKey != "":
		gemini, err := llm.NewGemini(ctx, geminiKey, "gemini-3.6-flash")
		if err != nil {
			log.Error("crear proveedor Gemini", "err", err)
			os.Exit(1)
		}
		chatProvider = llm.NewFallbackProvider(gemini, llm.NewGroq(groqKey, os.Getenv("GROQ_MODEL")))
		llmDesc = "gemini (primario) + groq (respaldo)"

	case geminiKey != "":
		gemini, err := llm.NewGemini(ctx, geminiKey, "gemini-3.6-flash")
		if err != nil {
			log.Error("crear proveedor Gemini", "err", err)
			os.Exit(1)
		}
		chatProvider = gemini
		llmDesc = "gemini"

	default: // solo groqKey
		chatProvider = llm.NewGroq(groqKey, os.Getenv("GROQ_MODEL"))
		llmDesc = "groq"
	}

	// Embeddings del RAG: dependen de Gemini (Groq no ofrece modelo de
	// embeddings). En modo solo-Groq se instala un placeholder que devuelve un
	// error descriptivo: el chat funciona, el RAG de documentos queda
	// indisponible (sin panics).
	var geminiEmbed embedding.Embedder
	if geminiKey != "" {
		geminiEmbed, err = embedding.NewGemini(ctx, geminiKey, "")
		if err != nil {
			log.Error("crear proveedor de embeddings", "err", err)
			os.Exit(1)
		}
	} else {
		geminiEmbed = embedding.Unavailable{}
	}

	// --- Registro de tools (mismo wiring que cmd/demo) ----------------------
	// Todos los stores comparten el MISMO *pgxpool.Pool: el pool es thread-safe
	// y entrega una conexión distinta por llamada concurrente.
	appsStore := pg.NewAplicacionStore(pool)
	// El store de lotes se comparte entre la tool consultar_lotes y el
	// endpoint GET /api/v1/lotes: un solo pool, un solo estado.
	loteStore := pg.NewLoteStore(pool)
	// --- HITL: el service de aprobaciones une el store de solicitudes, el
	// applier (que materializa la aprobación en UNA transacción: re-validación
	// + decisión condicional + INSERT) y el auditor. TTL de 24h: la solicitud
	// muere sola si nadie la aprueba.
	// El resolver de identidad UUID (agro-iam) se comparte entre el middleware
	// HTTP (tenant del claim → id interno) y el service de approvals (sub del
	// claim → actor interno, acotado al tenant).
	tenantStore := pg.NewTenantStore(pool)
	approvalSvc := approval.New(
		pg2.NewApprovalStore(pool),
		pg2.NewApplier(pool),
		pg2.NewAuditor(pool),
		24*time.Hour,
	).SetUserResolver(tenantStore)
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

	ag := agent.New(chatProvider, reg, agent.Options{MaxIterations: 5, Router: router.NewReglasClasificador()})

	// --- HTTP ----------------------------------------------------------------
	verifier, err := auth.NewVerifier(jwtSecret)
	if err != nil {
		log.Error("configurar auth", "err", err)
		os.Exit(1)
	}
	srv := httpapi.New(ag, verifier, approvalSvc, loteStore, appsStore)
	// Accept-both del tenant: entero (demo) directo, UUID (agro-iam) resuelto
	// vía tenants.uuid. Sin esto, un token real de agro-iam daría 401.
	srv.SetTenantResolver(tenantStore)

	// Sin ReadTimeout global a propósito: el SSE del chat necesita conexiones
	// long-lived. El ReadHeaderTimeout frena a los clientes que no terminan de
	// mandar el header (slowloris); IdleTimeout recicla conexiones ociosas.
	httpServer := &http.Server{
		Addr:              ":" + port,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	log.Info("agro-agent arrancando", "port", port, "llm", llmDesc, "tools", reg.Names())

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
