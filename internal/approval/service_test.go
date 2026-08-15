package approval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/agro-agent/agro-agent/internal/domain"
	"github.com/agro-agent/agro-agent/internal/identity"
	"github.com/agro-agent/agro-agent/internal/tenant"
)

// -----------------------------------------------------------------------------
// Fakes de los puertos (Store/Resolver/Writer/Auditor) + reloj fijo.
// Los tests del caso de uso NO dependen de Postgres.
// -----------------------------------------------------------------------------

type fakeStore struct {
	byID             map[int64]Request
	nextID           int64
	markedExpiredTID domain.TenantID
	decisions        []struct {
		id        int64
		decidedBy int64
		status    Status
	}
}

func newFakeStore() *fakeStore {
	return &fakeStore{byID: map[int64]Request{}, nextID: 1}
}

func (f *fakeStore) Create(_ context.Context, tid domain.TenantID, actorID int64, action string, payload json.RawMessage, tokenHash string, expiresAt time.Time) (int64, error) {
	id := f.nextID
	f.nextID++
	f.byID[id] = Request{
		ID: id, TenantID: tid, ActorUserID: actorID, Action: action,
		Payload: payload, Status: StatusPending, TokenHash: tokenHash,
		ExpiresAt: expiresAt, CreatedAt: expiresAt.Add(-time.Hour),
	}
	return id, nil
}

func (f *fakeStore) GetByTenant(_ context.Context, tid domain.TenantID, id int64) (*Request, error) {
	r, ok := f.byID[id]
	if !ok || r.TenantID != tid {
		return nil, ErrNotFound
	}
	return &r, nil
}

func (f *fakeStore) ListByTenant(_ context.Context, tid domain.TenantID, status string) ([]Request, error) {
	var out []Request
	for _, r := range f.byID {
		if r.TenantID != tid {
			continue
		}
		if status != "" && string(r.Status) != status {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

func (f *fakeStore) MarkExpired(_ context.Context, tid domain.TenantID) (int, error) {
	f.markedExpiredTID = tid
	return 0, nil
}

func (f *fakeStore) Decide(_ context.Context, tid domain.TenantID, id, decidedBy int64, status Status) error {
	r, ok := f.byID[id]
	if !ok || r.TenantID != tid {
		return ErrNotFound
	}
	r.Status = status
	r.DecidedBy = &decidedBy
	now := time.Now()
	r.DecidedAt = &now
	f.byID[id] = r
	f.decisions = append(f.decisions, struct {
		id        int64
		decidedBy int64
		status    Status
	}{id, decidedBy, status})
	return nil
}

type fakeResolver struct {
	lotes     map[string]int64
	productos map[string]int64
	campanas  map[string]int64
}

func (r *fakeResolver) ResolveLoteID(_ context.Context, _ domain.TenantID, codigo string) (int64, error) {
	id, ok := r.lotes[codigo]
	if !ok {
		return 0, ErrNotFound
	}
	return id, nil
}

func (r *fakeResolver) ResolveProductoID(_ context.Context, _ domain.TenantID, nombre string) (int64, error) {
	id, ok := r.productos[nombre]
	if !ok {
		return 0, ErrNotFound
	}
	return id, nil
}

func (r *fakeResolver) ResolveCampanaID(_ context.Context, _ domain.TenantID, nombre string) (int64, error) {
	id, ok := r.campanas[nombre]
	if !ok {
		return 0, ErrNotFound
	}
	return id, nil
}

type fakeWriter struct {
	created []AplicacionInput
	app     domain.Aplicacion
}

func (w *fakeWriter) CreateAplicacion(_ context.Context, _ domain.TenantID, _ int64, in AplicacionInput) (domain.Aplicacion, error) {
	w.created = append(w.created, in)
	if w.app.ID == 0 {
		w.app.ID = 999
	}
	return w.app, nil
}

type fakeAuditor struct {
	records int
	err     error // inyectar un error para probar fail-open
}

func (a *fakeAuditor) Record(_ context.Context, _ domain.TenantID, _ int64, _, _ string, _, _ any) error {
	a.records++
	return a.err
}

// -----------------------------------------------------------------------------
// Helpers compartidos
// -----------------------------------------------------------------------------

// hashToken reproduce el hash que el service espera en el store.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// mustPayload marshalea un AplicacionPayload válido para sembrar el store.
func mustPayload(t *testing.T, p AplicacionPayload) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return raw
}

// seedPending siembra una solicitud pendiente con token conocido y expiry futuro.
func seedPending(t *testing.T, s *fakeStore, tid domain.TenantID, token string) int64 {
	t.Helper()
	payload := mustPayload(t, AplicacionPayload{
		LoteCodigo: "12", Producto: "Glifosato 48%", Campana: "2026/2027",
		Dosis: 3.0, UnidadDosis: "L/ha", FechaPlanificada: "2026-08-20",
	})
	id, err := s.Create(context.Background(), tid, 1, "programar_aplicacion", payload, hashToken(token), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("seed request: %v", err)
	}
	return id
}

// ctxActor arma un contexto con tenant + actor (user/role) como el middleware.
func ctxActor(tid domain.TenantID, userID, role string) context.Context {
	ctx := tenant.WithID(context.Background(), tid)
	return identity.WithUserRole(ctx, userID, role)
}

// fixedClock devuelve el service con un reloj fijo (para vencimientos) y ttl fijo.
func newService(store Store, resolver Resolver, writer ApplicationWriter, auditor Auditor, now time.Time) *Service {
	svc := New(store, resolver, writer, auditor, time.Hour)
	svc.now = func() time.Time { return now }
	return svc
}

// -----------------------------------------------------------------------------
// CreateRequest
// -----------------------------------------------------------------------------

func TestCreateRequest_GeneraTokenYHash(t *testing.T) {
	store := newFakeStore()
	svc := newService(store, nil, nil, nil, time.Now())

	req, err := svc.CreateRequest(ctxActor(1, "42", "productor"), "programar_aplicacion", mustPayload(t, AplicacionPayload{LoteCodigo: "12"}))
	if err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	if len(req.Token) != 64 {
		t.Errorf("token no es 64 hex: %q", req.Token)
	}
	if req.Status != StatusPending || req.Action != "programar_aplicacion" {
		t.Errorf("solicitud mal armada: %+v", req)
	}
	// El store recibió el HASH (nunca el token plano) y con el ttl aplicado.
	persisted := store.byID[req.ID]
	if persisted.TokenHash == "" || persisted.TokenHash == req.Token {
		t.Errorf("el store debe guardar el hash, no el token: hash=%q", persisted.TokenHash)
	}
	if persisted.TokenHash != hashToken(req.Token) {
		t.Errorf("hash no coincide: %q vs %q", persisted.TokenHash, hashToken(req.Token))
	}
	if !persisted.ExpiresAt.After(time.Now()) {
		t.Errorf("expires_at no aplicó el ttl: %v", persisted.ExpiresAt)
	}
	// El Request devuelto SÍ trae el token (viaja solo en el retorno de creación).
	if req.Token == "" {
		t.Error("el token debe viajar en el retorno de CreateRequest")
	}
}

func TestCreateRequest_LeeTenantDelContexto(t *testing.T) {
	store := newFakeStore()
	svc := newService(store, nil, nil, nil, time.Now())

	if _, err := svc.CreateRequest(ctxActor(2, "42", "productor"), "programar_aplicacion", mustPayload(t, AplicacionPayload{LoteCodigo: "12"})); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	// Aislamiento: con ctx de tenant 2, el store recibe tid 2.
	got := store.byID[1]
	if got.TenantID != 2 {
		t.Errorf("el store recibió tenant %d, esperaba 2", got.TenantID)
	}
}

func TestCreateRequest_SinActorFalla(t *testing.T) {
	svc := newService(newFakeStore(), nil, nil, nil, time.Now())
	// Contexto con tenant pero SIN identity: fail-closed, la solicitud no
	// puede quedar huérfana de autor.
	ctx := tenant.WithID(context.Background(), domain.TenantID(1))
	if _, err := svc.CreateRequest(ctx, "programar_aplicacion", mustPayload(t, AplicacionPayload{LoteCodigo: "12"})); err == nil {
		t.Fatal("esperaba error sin user_id en el contexto")
	}
}

// -----------------------------------------------------------------------------
// Approve
// -----------------------------------------------------------------------------

func TestApprove_Feliz(t *testing.T) {
	store := newFakeStore()
	resolver := &fakeResolver{lotes: map[string]int64{"12": 1}, productos: map[string]int64{"Glifosato 48%": 1}, campanas: map[string]int64{"2026/2027": 3}}
	writer := &fakeWriter{app: domain.Aplicacion{ID: 42}}
	auditor := &fakeAuditor{}
	svc := newService(store, resolver, writer, auditor, time.Now())

	id := seedPending(t, store, 1, "tokensecreto")
	app, err := svc.Approve(ctxActor(1, "2", "agronomo"), id, "tokensecreto")
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if app.ID != 42 {
		t.Errorf("no devolvió la aplicación creada: %+v", app)
	}
	// Writer llamado con los IDs RESUELTOS (re-validación del contexto).
	if len(writer.created) != 1 {
		t.Fatalf("writer no llamado: %d", len(writer.created))
	}
	in := writer.created[0]
	if in.LoteID != 1 || in.ProductoID != 1 || in.CampanaID != 3 {
		t.Errorf("IDs no resueltos: %+v", in)
	}
	if in.Dosis != 3.0 || in.FechaPlanificada != "2026-08-20" || in.UnidadDosis != "L/ha" {
		t.Errorf("payload no revalidado: %+v", in)
	}
	// Decide llamado con estado ejecutado (aprobación == ejecución en este slice).
	if len(store.decisions) != 1 || store.decisions[0].status != StatusExecuted {
		t.Errorf("Decide no llamado con ejecutado: %+v", store.decisions)
	}
	if auditor.records != 1 {
		t.Errorf("auditor no llamado: %d", auditor.records)
	}
}

func TestApprove_TokenIncorrecto(t *testing.T) {
	store := newFakeStore()
	writer := &fakeWriter{}
	svc := newService(store, &fakeResolver{}, writer, &fakeAuditor{}, time.Now())

	id := seedPending(t, store, 1, "tokensecreto")
	if _, err := svc.Approve(ctxActor(1, "2", "agronomo"), id, "token-mal"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("esperaba ErrInvalidToken, obtuve %v", err)
	}
	if len(writer.created) != 0 {
		t.Error("con token inválido NO se debe llamar al writer")
	}
}

func TestApprove_Vencida(t *testing.T) {
	store := newFakeStore()
	now := time.Now()
	// Seed con expiry en el PASADO respecto del reloj fijo del service.
	payload := mustPayload(t, AplicacionPayload{
		LoteCodigo: "12", Producto: "Glifosato 48%", Campana: "2026/2027",
		Dosis: 3.0, FechaPlanificada: "2026-08-20",
	})
	id, _ := store.Create(context.Background(), 1, 1, "programar_aplicacion", payload, hashToken("tokensecreto"), now.Add(-time.Minute))
	svc := newService(store, &fakeResolver{}, &fakeWriter{}, &fakeAuditor{}, now)

	if _, err := svc.Approve(ctxActor(1, "2", "agronomo"), id, "tokensecreto"); !errors.Is(err, ErrExpired) {
		t.Fatalf("esperaba ErrExpired, obtuve %v", err)
	}
}

func TestApprove_NoPendiente(t *testing.T) {
	store := newFakeStore()
	// La primera aprobación debe llegar hasta Decide: el resolver resuelve todo.
	resolver := &fakeResolver{lotes: map[string]int64{"12": 1}, productos: map[string]int64{"Glifosato 48%": 1}, campanas: map[string]int64{"2026/2027": 3}}
	svc := newService(store, resolver, &fakeWriter{app: domain.Aplicacion{ID: 42}}, &fakeAuditor{}, time.Now())

	id := seedPending(t, store, 1, "tokensecreto")
	// La ejecuta una vez (queda 'ejecutado'); la segunda aprobación es ErrNotPending.
	if _, err := svc.Approve(ctxActor(1, "2", "agronomo"), id, "tokensecreto"); err != nil {
		t.Fatalf("primera aprobación: %v", err)
	}
	if _, err := svc.Approve(ctxActor(1, "2", "agronomo"), id, "tokensecreto"); !errors.Is(err, ErrNotPending) {
		t.Fatalf("esperaba ErrNotPending, obtuve %v", err)
	}
}

func TestApprove_LoteNoResuelve(t *testing.T) {
	store := newFakeStore()
	// Resolver SIN el lote "12" del payload: la re-validación debe fallar.
	writer := &fakeWriter{}
	svc := newService(store, &fakeResolver{}, writer, &fakeAuditor{}, time.Now())

	id := seedPending(t, store, 1, "tokensecreto")
	_, err := svc.Approve(ctxActor(1, "2", "agronomo"), id, "tokensecreto")
	if err == nil {
		t.Fatal("esperaba error de re-validación")
	}
	if !strings.Contains(err.Error(), "no existe o no pertenece al tenant") {
		t.Errorf("error sin explicar la causa: %v", err)
	}
	if len(writer.created) != 0 {
		t.Error("si el lote no resuelve, NO se llama al writer")
	}
}

func TestApprove_PayloadManipulado(t *testing.T) {
	store := newFakeStore()
	// Payload con un campo fuera del contrato tipado: solo entra manipulando
	// la DB; la re-validación fail-closed lo rechaza.
	manipulado := json.RawMessage(`{"lote_codigo":"12","producto":"Glifosato 48%","campana":"2026/2027","dosis":3,"fecha_planificada":"2026-08-20","tenant_id":2}`)
	id, _ := store.Create(context.Background(), 1, 1, "programar_aplicacion", manipulado, hashToken("tokensecreto"), time.Now().Add(time.Hour))
	svc := newService(store, &fakeResolver{}, &fakeWriter{}, &fakeAuditor{}, time.Now())

	if _, err := svc.Approve(ctxActor(1, "2", "agronomo"), id, "tokensecreto"); err == nil {
		t.Fatal("esperaba error por payload con campo desconocido")
	}
}

func TestApprove_AuditorFailOpen(t *testing.T) {
	store := newFakeStore()
	resolver := &fakeResolver{lotes: map[string]int64{"12": 1}, productos: map[string]int64{"Glifosato 48%": 1}, campanas: map[string]int64{"2026/2027": 3}}
	writer := &fakeWriter{app: domain.Aplicacion{ID: 42}}
	// La auditoría FALLA: el approve debe seguir exitoso (fail-open).
	auditor := &fakeAuditor{err: errors.New("audit db caída")}
	svc := newService(store, resolver, writer, auditor, time.Now())

	id := seedPending(t, store, 1, "tokensecreto")
	if _, err := svc.Approve(ctxActor(1, "2", "agronomo"), id, "tokensecreto"); err != nil {
		t.Fatalf("un fallo de auditoría no debe tumbar el approve: %v", err)
	}
	if auditor.records != 1 {
		t.Errorf("el auditor igual fue llamado: %d", auditor.records)
	}
}

// -----------------------------------------------------------------------------
// Reject y List
// -----------------------------------------------------------------------------

func TestReject_Feliz(t *testing.T) {
	store := newFakeStore()
	auditor := &fakeAuditor{}
	svc := newService(store, nil, nil, auditor, time.Now())

	id := seedPending(t, store, 1, "tokensecreto")
	if err := svc.Reject(ctxActor(1, "2", "agronomo"), id, "tokensecreto"); err != nil {
		t.Fatalf("Reject: %v", err)
	}
	if len(store.decisions) != 1 || store.decisions[0].status != StatusRejected {
		t.Errorf("Decide no llamado con rechazado: %+v", store.decisions)
	}
	if auditor.records != 1 {
		t.Errorf("auditor no llamado: %d", auditor.records)
	}
}

func TestReject_TokenIncorrecto(t *testing.T) {
	store := newFakeStore()
	svc := newService(store, nil, nil, &fakeAuditor{}, time.Now())

	id := seedPending(t, store, 1, "tokensecreto")
	if err := svc.Reject(ctxActor(1, "2", "agronomo"), id, "mal"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("esperaba ErrInvalidToken, obtuve %v", err)
	}
	if len(store.decisions) != 0 {
		t.Error("token inválido no decide nada")
	}
}

func TestList_MarcaVencidas(t *testing.T) {
	store := newFakeStore()
	svc := newService(store, nil, nil, nil, time.Now())

	if _, err := svc.List(ctxActor(1, "42", "productor"), ""); err != nil {
		t.Fatalf("List: %v", err)
	}
	if store.markedExpiredTID != 1 {
		t.Errorf("MarkExpired no llamado con el tenant del ctx: %d", store.markedExpiredTID)
	}
}

func TestList_FiltraPorEstado(t *testing.T) {
	store := newFakeStore()
	svc := newService(store, nil, nil, nil, time.Now())

	seedPending(t, store, 1, "tokensecreto")
	reqs, err := svc.List(ctxActor(1, "42", "productor"), "pendiente")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(reqs) != 1 || reqs[0].Status != StatusPending {
		t.Fatalf("filtro por estado roto: %+v", reqs)
	}
}