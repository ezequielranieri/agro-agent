package approval

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/agro-agent/agro-agent/internal/domain"
	"github.com/agro-agent/agro-agent/internal/identity"
	"github.com/agro-agent/agro-agent/internal/tenant"
)

// defaultTTL es la vigencia de una solicitud cuando New recibe ttl <= 0.
// La solicitud "muere sola": vencida no es aprobable aunque el token sea real.
const defaultTTL = 24 * time.Hour

// Service es el caso de uso del HITL. Depende solo de puertos (Store,
// Resolver, ApplicationWriter, Auditor): no conoce Postgres ni HTTP.
type Service struct {
	store    Store
	resolver Resolver
	writer   ApplicationWriter
	auditor  Auditor
	ttl      time.Duration
	now      func() time.Time // reloj inyectable: los tests fijan fechas
}

// New arma el service. ttl <= 0 cae al default (24h); el reloj por defecto es
// time.Now, los tests inyectan uno fijo.
func New(store Store, resolver Resolver, writer ApplicationWriter, auditor Auditor, ttl time.Duration) *Service {
	if ttl <= 0 {
		ttl = defaultTTL
	}
	now := time.Now
	return &Service{store: store, resolver: resolver, writer: writer, auditor: auditor, ttl: ttl, now: now}
}

// newToken genera el token opaco de aprobación: 32 bytes aleatorios en hex
// (64 chars). No lleva significado: es un secreto de un solo uso presentado
// por el humano. Retorna también su hash sha256 (lo único que persiste).
func newToken() (token, hash string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("approval: generar token: %w", err)
	}
	token = hex.EncodeToString(buf)
	sum := sha256.Sum256([]byte(token))
	return token, hex.EncodeToString(sum[:]), nil
}

// actorFromCtx extrae y valida el actor del contexto. El user_id viaja como
// string (claim JWT); acá se convierte a int64 para la DB. Sin actor, una
// solicitud no tiene dueño: fail-closed.
func actorFromCtx(ctx context.Context) (int64, error) {
	actorID := identity.UserIDFrom(ctx)
	if actorID == "" {
		return 0, errors.New("approval: no hay user_id en el contexto")
	}
	actorInt, err := strconv.ParseInt(actorID, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("approval: user_id no numérico: %w", err)
	}
	return actorInt, nil
}

// record es nil-safe: el service funciona sin auditor (tests, demo local) y
// la auditoría jamás tumba el flujo de negocio (fail-open).
func (s *Service) record(ctx context.Context, tid domain.TenantID, actorID int64, action, tool string, params, result any) {
	if s.auditor == nil {
		return
	}
	if err := s.auditor.Record(ctx, tid, actorID, action, tool, params, result); err != nil {
		slog.Warn("approval: fallo de auditoría (ignorado)", "err", err)
	}
}

// CreateRequest crea la solicitud PENDIENTE. La tool NO inserta la aplicación:
// solo registra la intención y devuelve el token al solicitante. El store
// persiste únicamente el hash, así el token no puede filtrarse desde la DB.
func (s *Service) CreateRequest(ctx context.Context, action string, payload json.RawMessage) (Request, error) {
	tid, err := tenant.FromContext(ctx)
	if err != nil {
		return Request{}, fmt.Errorf("approval: crear solicitud: %w", err)
	}
	actorInt, err := actorFromCtx(ctx)
	if err != nil {
		return Request{}, err
	}

	token, hash, err := newToken()
	if err != nil {
		return Request{}, err
	}

	expiresAt := s.now().Add(s.ttl)
	id, err := s.store.Create(ctx, tid, actorInt, action, payload, hash, expiresAt)
	if err != nil {
		return Request{}, fmt.Errorf("approval: crear solicitud: %w", err)
	}

	// La auditoría de la CREACIÓN es fail-open: la solicitud ya quedó
	// registrada; un fallo de audit no debe tumbar la creación (WARN).
	// La rendición de cuentas arranca desde el pedido, no desde la decisión.
	s.record(ctx, tid, actorInt, "approval.crear", "programar_aplicacion", payload, map[string]any{"approval_id": id})

	return Request{
		ID:          id,
		TenantID:    tid,
		ActorUserID: actorInt,
		Action:      action,
		Payload:     payload,
		Status:      StatusPending,
		Token:       token, // el token SOLO viaja en el retorno de la creación
		ExpiresAt:   expiresAt,
		CreatedAt:   s.now(),
	}, nil
}
// Primero marca vencidas: una solicitud cuyo expires_at pasó NO debe seguir
// apareciendo como pendiente.
func (s *Service) List(ctx context.Context, status string) ([]Request, error) {
	tid, err := tenant.FromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("approval: listar: %w", err)
	}
	if _, err := s.store.MarkExpired(ctx, tid); err != nil {
		return nil, fmt.Errorf("approval: marcar vencidas: %w", err)
	}
	reqs, err := s.store.ListByTenant(ctx, tid, status)
	if err != nil {
		return nil, fmt.Errorf("approval: listar: %w", err)
	}
	return reqs, nil
}

// Approve aprueba y EJECUTA la solicitud en el mismo flujo. El orden es
// deliberado: primero se valida el estado (pendiente y vigente), después el
// token, y recién entonces se RE-VALIDA el contexto del payload original para
// resolver lote/producto/campaña DENTRO del tenant. Una solicitud manipulada
// o de otra cooperativa falla acá, no en el INSERT.
func (s *Service) Approve(ctx context.Context, id int64, token string) (domain.Aplicacion, error) {
	tid, err := tenant.FromContext(ctx)
	if err != nil {
		return domain.Aplicacion{}, fmt.Errorf("approval: aprobar: %w", err)
	}
	deciderInt, err := actorFromCtx(ctx)
	if err != nil {
		return domain.Aplicacion{}, err
	}

	req, err := s.store.GetByTenant(ctx, tid, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return domain.Aplicacion{}, ErrNotFound
		}
		return domain.Aplicacion{}, fmt.Errorf("approval: aprobar: %w", err)
	}
	if req.Status != StatusPending {
		return domain.Aplicacion{}, ErrNotPending
	}
	if req.ExpiresAt.Before(s.now()) {
		return domain.Aplicacion{}, ErrExpired
	}

	// Comparación en tiempo constante sobre los bytes hex: evita medir por
	// timing cuántos caracteres del hash coinciden.
	sum := sha256.Sum256([]byte(token))
	if subtle.ConstantTimeCompare([]byte(hex.EncodeToString(sum[:])), []byte(req.TokenHash)) != 1 {
		return domain.Aplicacion{}, ErrInvalidToken
	}

	payload, err := decodePayload(req.Payload)
	if err != nil {
		return domain.Aplicacion{}, fmt.Errorf("approval: re-validación: %w", err)
	}

	loteID, err := s.resolver.ResolveLoteID(ctx, tid, payload.LoteCodigo)
	if err != nil {
		return domain.Aplicacion{}, fmt.Errorf("re-validación: el lote %q no existe o no pertenece al tenant", payload.LoteCodigo)
	}
	productoID, err := s.resolver.ResolveProductoID(ctx, tid, payload.Producto)
	if err != nil {
		return domain.Aplicacion{}, fmt.Errorf("re-validación: el producto %q no existe o no pertenece al tenant", payload.Producto)
	}
	campanaID, err := s.resolver.ResolveCampanaID(ctx, tid, payload.Campana)
	if err != nil {
		return domain.Aplicacion{}, fmt.Errorf("re-validación: la campaña %q no existe o no pertenece al tenant", payload.Campana)
	}

	app, err := s.writer.CreateAplicacion(ctx, tid, deciderInt, AplicacionInput{
		LoteID:           loteID,
		CampanaID:        campanaID,
		ProductoID:       productoID,
		Dosis:            payload.Dosis,
		UnidadDosis:      payload.UnidadDosis,
		FechaPlanificada: payload.FechaPlanificada,
		Notas:            payload.Notas,
	})
	if err != nil {
		return domain.Aplicacion{}, fmt.Errorf("approval: crear aplicación: %w", err)
	}

	// DECISIÓN DE DISEÑO: la aprobación ejecuta en el MISMO flujo, por eso el
	// estado final es 'ejecutado' (no 'aprobado'). El estado 'aprobado' queda
	// reservado a un futuro worker que ejecute en diferido; hoy aprobar ==
	// ejecutar, y el registro debe reflejarlo.
	if err := s.store.Decide(ctx, tid, id, deciderInt, StatusExecuted); err != nil {
		return domain.Aplicacion{}, fmt.Errorf("approval: marcar ejecutado: %w", err)
	}

	// La auditoría es fail-open: la aplicación ya se creó y decidió; un fallo
	// de audit no debe tumbar el approve (solo se registra en WARN).
	s.record(ctx, tid, deciderInt, "approval.aprobar", "programar_aplicacion", req.Payload, app)

	return app, nil
}

// Reject rechaza la solicitud. Mismo preámbulo de validación que Approve
// (estado pendiente, vigente, token correcto): sin esas garantías, rechazar
// una solicitud que no está en juego no tiene sentido.
func (s *Service) Reject(ctx context.Context, id int64, token string) error {
	tid, err := tenant.FromContext(ctx)
	if err != nil {
		return fmt.Errorf("approval: rechazar: %w", err)
	}
	deciderInt, err := actorFromCtx(ctx)
	if err != nil {
		return err
	}

	req, err := s.store.GetByTenant(ctx, tid, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return ErrNotFound
		}
		return fmt.Errorf("approval: rechazar: %w", err)
	}
	if req.Status != StatusPending {
		return ErrNotPending
	}
	if req.ExpiresAt.Before(s.now()) {
		return ErrExpired
	}
	sum := sha256.Sum256([]byte(token))
	if subtle.ConstantTimeCompare([]byte(hex.EncodeToString(sum[:])), []byte(req.TokenHash)) != 1 {
		return ErrInvalidToken
	}

	if err := s.store.Decide(ctx, tid, id, deciderInt, StatusRejected); err != nil {
		return fmt.Errorf("approval: rechazar: %w", err)
	}
	// Fail-open, igual que en Approve: el rechazo ya se decidió.
	s.record(ctx, tid, deciderInt, "approval.rechazar", "programar_aplicacion", req.Payload, nil)
	return nil
}

// decodePayload re-parsea el payload guardado con DisallowUnknownFields
// (fail-closed): si la tool marshaleó un struct con el contrato tipado, un
// campo extra solo puede haber entrado por manipulación directa de la DB.
func decodePayload(raw json.RawMessage) (AplicacionPayload, error) {
	var p AplicacionPayload
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return AplicacionPayload{}, err
	}
	if err := p.Validate(); err != nil {
		return AplicacionPayload{}, err
	}
	return p, nil
}