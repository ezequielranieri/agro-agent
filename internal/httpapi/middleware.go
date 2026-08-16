package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/agro-agent/agro-agent/internal/domain"
	"github.com/agro-agent/agro-agent/internal/identity"
	"github.com/agro-agent/agro-agent/internal/tenant"
)

// requestIDKey es la clave del contexto para el id de correlación del request.
type requestIDKey struct{}

// requestIDFromCtx devuelve el request_id del contexto, o "?" si no llegó a
// generarse (defensa: el log es un dato, no el flujo).
func requestIDFromCtx(ctx context.Context) string {
	if v, ok := ctx.Value(requestIDKey{}).(string); ok && v != "" {
		return v
	}
	return "?"
}

// newRequestID genera un id corto de correlación (8 hex chars) con
// crypto/rand, sin dependencias. Si el OS no proveyera entropía (imposible en
// la práctica), cae a un timestamp para que el request jamás quede sin id.
func newRequestID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err == nil {
		return hex.EncodeToString(b[:])
	}
	return strconv.FormatInt(time.Now().UnixNano(), 16)
}

// writeJSONErr es el helper de errores JSON UNIFORMES: mismo shape, mismo
// status, sin filtrar el detalle interno al cliente. El detalle real se
// loguea aparte (ver los handlers).
func writeJSONErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// requireAuth valida el Bearer token y aísla el request en su tenant.
// Falla cerrado en el primer desvío: sin header, scheme mal, token inválido
// o tenant no numérico → el MISMO 401 uniforme.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authz := r.Header.Get("Authorization")
		const scheme = "Bearer "
		if !strings.HasPrefix(authz, scheme) {
			writeJSONErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		token := strings.TrimPrefix(authz, scheme)

		claims, err := s.verifier.Verify(token)
		if err != nil {
			writeJSONErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		// El tenant viaja como int64 (el claim es string en el JWT). Si el
		// emisor mandó algo no numérico, el token es inválido: nunca dejar
		// pasar un tenant fantasma.
		tid, err := strconv.ParseInt(claims.TenantID, 10, 64)
		if err != nil {
			writeJSONErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		ctx := tenant.WithID(r.Context(), domain.TenantID(tid))
		// El actor (user_id/role) vive en internal/identity: las tools del HITL
		// lo leen sin depender del transporte HTTP (regla de la hexagonalidad).
		ctx = identity.WithUserRole(ctx, claims.UserID, claims.Role)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requireRole autoriza por rol del actor autenticado. DEBE encadenarse DESPUÉS
// de requireAuth: sin ese middleware el contexto no tiene identity.RoleFrom y
// cualquier request sin token caería en 403 en vez del 401 uniforme.
func (s *Server) requireRole(allowed ...string) func(http.Handler) http.Handler {
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, r := range allowed {
		allowedSet[r] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			role := identity.RoleFrom(r.Context())
			if _, ok := allowedSet[role]; !ok {
				// 403 uniforme: no se filtra qué rol es necesario, solo que
				// el del actor no alcanza para esta acción.
				writeJSONErr(w, http.StatusForbidden, "forbidden")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// statusWriter captura el status code para el log: net/http solo expone el
// código en WriteHeader, y queremos el real (no el default 200).
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// logging registra method, path, status y duración de cada request. Acepta
// que WriteHeader se llame una sola vez (los handlers escriben un solo
// status); si no se llama, el status queda 200.
//
// Correlación: si el cliente/proxy mandó un X-Request-ID se respeta (así un
// request que atraviesa varios servicios se sigue con el mismo id); si no,
// se genera uno local. El id viaja al log como request_id, se guarda en el
// contexto (lo lee recover para el log de panics) y se devuelve al cliente
// en el header de respuesta (clave para depurar un stream SSE cuyo cliente
// se desconectó).
func (s *Server) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rid := r.Header.Get("X-Request-ID")
		if rid == "" {
			rid = newRequestID()
		}
		w.Header().Set("X-Request-ID", rid)
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		ctx := context.WithValue(r.Context(), requestIDKey{}, rid)
		next.ServeHTTP(sw, r.WithContext(ctx))
		s.log.Info("http",
			"method", r.Method,
			"path", r.URL.Path,
			"status", sw.status,
			"duration_ms", time.Since(start).Milliseconds(),
			"request_id", rid,
		)
	})
}

// recover convierte un panic en un 500: un handler que revienta NO debe
// tumbar el server (le costaría la sesión a todos los tenants).
func (s *Server) recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.log.Error("http: panic recuperado", "err", rec, "path", r.URL.Path, "request_id", requestIDFromCtx(r.Context()))
				writeJSONErr(w, http.StatusInternalServerError, "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// errDecodeBody marca el 400 uniforme de payload inválido. Se usa en los
// handlers para distinguir "el cliente mandó basura" de un fallo interno.
var errDecodeBody = errors.New("invalid request")
