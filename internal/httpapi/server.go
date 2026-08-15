// Package httpapi es el transporte HTTP del agente (adaptador de la
// hexagonalidad): convierte requests JSON en llamadas al orquestador y expone
// el resultado como JSON o SSE. NO contiene lógica de negocio ni de tools:
// delega todo en internal/agent y solo maneja la forma del protocolo.
//
// Stack: net/http estándar, sin frameworks. Cada request pasa por
// logging → recover → auth (salvo rutas públicas como /healthz).
package httpapi

import (
	"log/slog"
	"net/http"
	"os"

	"github.com/agro-agent/agro-agent/internal/agent"
	"github.com/agro-agent/agro-agent/internal/approval"
	"github.com/agro-agent/agro-agent/internal/auth"
	"github.com/agro-agent/agro-agent/internal/store"
)

// Server concentra las dependencias del transporte. El *agent.Agent es
// COMPARTIDO entre requests y no tiene estado por request: por eso el handler
// de chat construye un agente efímero por request (con su propio OnEvent)
// usando los getters del orquestador, en vez de mutar este Server.
type Server struct {
	agent     *agent.Agent
	verifier  *auth.Verifier
	approvals *approval.Service
	loteStore store.LoteStore
	log       *slog.Logger
}

// New arma el server. El logger por defecto va a stdout con formato texto
// (simple de leer en contenedores); los tests pueden inyectar uno distinto.
// approvals puede ser nil (p. ej. tests de chat): los handlers de approvals
// responden 501 y el resto del API no se ve afectado. loteStore es requerido:
// GET /api/v1/lotes lo consulta en cada request.
func New(ag *agent.Agent, verifier *auth.Verifier, approvals *approval.Service, loteStore store.LoteStore) *Server {
	return &Server{
		agent:     ag,
		verifier:  verifier,
		approvals: approvals,
		loteStore: loteStore,
		log:       slog.New(slog.NewTextHandler(os.Stdout, nil)),
	}
}

// Handler devuelve el mux completo con los middlewares aplicados.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.Handle("POST /api/v1/chat", s.requireAuth(http.HandlerFunc(s.handleChat)))
	// HITL: la lectura de solicitudes es para todo tenant autenticado; aprobar
	// y rechazar (escritura) queda restringida a admin/agronomo. requireRole
	// SIEMPRE va encadenado después de requireAuth (ver middleware.go).
	mux.Handle("GET /api/v1/approvals", s.requireAuth(http.HandlerFunc(s.handleListApprovals)))
	mux.Handle("GET /api/v1/lotes", s.requireAuth(http.HandlerFunc(s.handleListLotes)))
	mux.Handle("POST /api/v1/approvals/{id}/approve", s.requireAuth(s.requireRole("admin", "agronomo")(http.HandlerFunc(s.handleApprove))))
	mux.Handle("POST /api/v1/approvals/{id}/reject", s.requireAuth(s.requireRole("admin", "agronomo")(http.HandlerFunc(s.handleReject))))
	// logging y recover envuelven TODO el mux: ningún request los esquiva.
	return s.recover(s.logging(mux))
}

// handleHealthz es el latido de orquestación (k8s, LB): no requiere auth ni
// toca estado, por eso no lee contexto de tenant ni de usuario.
func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}
