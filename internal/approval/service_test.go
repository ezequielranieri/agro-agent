package approval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agro-agent/agro-agent/internal/domain"
	"github.com/agro-agent/agro-agent/internal/identity"
	"github.com/agro-agent/agro-agent/internal/tenant"
)

// -----------------------------------------------------------------------------
// Fakes de los puertos (Store/Applier/Auditor) + reloj fijo.
// Los tests del caso de uso NO dependen de Postgres.
// -----------------------------------------------------------------------------

// fakeStore es thread-safe: el test de carrera dispara approves concurrentes
// que leen (GetByTenant) y escriben (Decide) sobre el mismo mapa. Decide
// replica la guarda condicional de la DB: solo una fila 'pendiente' cambia.
type fakeStore struct {
	mu               sync.Mutex
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
	f.mu.Lock()
	defer f.mu.Unlock()
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
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.byID[id]
	if !ok || r.TenantID != tid {
		return nil, ErrNotFound
	}
	return &r, nil
}

func (f *fakeStore) ListByTenant(_ context.Context, tid domain.TenantID, status string) ([]Request, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
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
	f.mu.Lock()
	defer f.mu.Unlock()
	f.markedExpiredTID = tid
	return 0, nil
}

// Decide replica la transición condicional de la DB: solo una fila pendiente
// puede cambiar de estado. Un decide sobre una solicitud ya decidida falla
// con ErrNotPending (lo que el transporte mapea a 409).
func (f *fakeStore) Decide(_ context.Context, tid domain.TenantID, id, decidedBy int64, status Status) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.byID[id]
	if !ok || r.TenantID != tid {
		return ErrNotFound
	}
	if r.Status != StatusPending {
		return ErrNotPending
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

// applyCall registra una materialización: la resolución y la aplicación
// creada, para que los tests verifiquen la re-validación del contexto.
type applyCall struct {
	id         int64
	decidedBy  int64
	loteID     int64
	productoID int64
	campanaID  int64
	payload    AplicacionPayload
}

// fakeApplier modela la transacción del adaptador pg: re-valida el contexto,
// aplica la guarda condicional (solo una solicitud pendiente gana) e inserta
// la aplicación. El mutex lo comparte con el fakeStore, así la secuencia
// GetByTenant→Apply del service se serializa igual que el bloqueo de fila del
// UPDATE en Postgres.
type fakeApplier struct {
	store     *fakeStore
	lotes     map[string]int64
	productos map[string]int64
	campanas  map[string]int64
	app       domain.Aplicacion
	calls     []applyCall
	created   []domain.Aplicacion
}

func newFakeApplier(store *fakeStore) *fakeApplier {
	return &fakeApplier{
		store:     store,
		lotes:     map[string]int64{},
		productos: map[string]int64{},
		campanas:  map[string]int64{},
	}
}

func (a *fakeApplier) Apply(_ context.Context, tid domain.TenantID, id, decidedBy int64, p AplicacionPayload) (domain.Aplicacion, error) {
	a.store.mu.Lock()
	defer a.store.mu.Unlock()

	// Guarda de carrera idéntica a la DB: la solicitud debe seguir pendiente.
	r, ok := a.store.byID[id]
	if !ok || r.TenantID != tid {
		return domain.Aplicacion{}, ErrNotFound
	}
	if r.Status != StatusPending {
		return domain.Aplicacion{}, ErrNotPending
	}

	// Re-validación del contexto (resuelve acotado al tenant).
	loteID, ok := a.lotes[p.LoteCodigo]
	if !ok {
		return domain.Aplicacion{}, fmt.Errorf("re-validación: el lote %q no existe o no pertenece al tenant", p.LoteCodigo)
	}
	productoID, ok := a.productos[p.Producto]
	if !ok {
		return domain.Aplicacion{}, fmt.Errorf("re-validación: el producto %q no existe o no pertenece al tenant", p.Producto)
	}
	campanaID, ok := a.campanas[p.Campana]
	if !ok {
		return domain.Aplicacion{}, fmt.Errorf("re-validación: la campaña %q no existe o no pertenece al tenant", p.Campana)
	}

	// El INSERT solo ocurre si la guarda pasó: el perdedor de la carrera no
	// crea ninguna fila de aplicación.
	app := a.app
	if app.ID == 0 {
		app.ID = 999
	}
	app.LoteID = loteID
	app.CampanaID = campanaID
	a.calls = append(a.calls, applyCall{id: id, decidedBy: decidedBy, loteID: loteID, productoID: productoID, campanaID: campanaID, payload: p})
	a.created = append(a.created, app)

	// Marca la solicitud como ejecutada (igual que el UPDATE del pg adapter).
	r.Status = StatusExecuted
	r.DecidedBy = &decidedBy
	now := time.Now()
	r.DecidedAt = &now
	a.store.byID[id] = r

	return app, nil
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
func newService(store Store, applier Applier, auditor Auditor, now time.Time) *Service {
	svc := New(store, applier, auditor, time.Hour)
	svc.now = func() time.Time { return now }
	return svc
}

// resolvedApplier devuelve un applier listo para el approve feliz: resuelve el
// lote/producto/campaña del payload de seedPending.
func resolvedApplier(store *fakeStore) *fakeApplier {
	a := newFakeApplier(store)
	a.lotes["12"] = 1
	a.productos["Glifosato 48%"] = 1
	a.campanas["2026/2027"] = 3
	return a
}

// -----------------------------------------------------------------------------
// CreateRequest
// -----------------------------------------------------------------------------

func TestCreateRequest_GeneraTokenYHash(t *testing.T) {
	store := newFakeStore()
	svc := newService(store, nil, nil, time.Now())

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
	svc := newService(store, nil, nil, time.Now())

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
	svc := newService(newFakeStore(), nil, nil, time.Now())
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
	applier := resolvedApplier(store)
	applier.app = domain.Aplicacion{ID: 42}
	auditor := &fakeAuditor{}
	svc := newService(store, applier, auditor, time.Now())

	id := seedPending(t, store, 1, "tokensecreto")
	app, err := svc.Approve(ctxActor(1, "2", "agronomo"), id, "tokensecreto")
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if app.ID != 42 {
		t.Errorf("no devolvió la aplicación creada: %+v", app)
	}
	// Applier llamado con los IDs RESUELTOS (re-validación del contexto).
	if len(applier.calls) != 1 {
		t.Fatalf("applier no llamado: %d", len(applier.calls))
	}
	call := applier.calls[0]
	if call.loteID != 1 || call.productoID != 1 || call.campanaID != 3 {
		t.Errorf("IDs no resueltos: %+v", call)
	}
	if call.payload.Dosis != 3.0 || call.payload.FechaPlanificada != "2026-08-20" || call.payload.UnidadDosis != "L/ha" {
		t.Errorf("payload no revalidado: %+v", call.payload)
	}
	// La solicitud quedó ejecutada (aprobación == ejecución en este slice).
	persisted := store.byID[id]
	if persisted.Status != StatusExecuted {
		t.Errorf("la solicitud no quedó ejecutada: %s", persisted.Status)
	}
	if auditor.records != 1 {
		t.Errorf("auditor no llamado: %d", auditor.records)
	}
}

func TestApprove_TokenIncorrecto(t *testing.T) {
	store := newFakeStore()
	applier := newFakeApplier(store)
	svc := newService(store, applier, &fakeAuditor{}, time.Now())

	id := seedPending(t, store, 1, "tokensecreto")
	if _, err := svc.Approve(ctxActor(1, "2", "agronomo"), id, "token-mal"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("esperaba ErrInvalidToken, obtuve %v", err)
	}
	if len(applier.created) != 0 {
		t.Error("con token inválido NO se debe llamar al applier")
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
	svc := newService(store, newFakeApplier(store), &fakeAuditor{}, now)

	if _, err := svc.Approve(ctxActor(1, "2", "agronomo"), id, "tokensecreto"); !errors.Is(err, ErrExpired) {
		t.Fatalf("esperaba ErrExpired, obtuve %v", err)
	}
}

func TestApprove_NoPendiente(t *testing.T) {
	store := newFakeStore()
	// La primera aprobación debe llegar hasta el applier: el resolver resuelve todo.
	applier := resolvedApplier(store)
	applier.app = domain.Aplicacion{ID: 42}
	svc := newService(store, applier, &fakeAuditor{}, time.Now())

	id := seedPending(t, store, 1, "tokensecreto")
	// La ejecuta una vez (queda 'ejecutado'); la segunda aprobación es ErrNotPending.
	if _, err := svc.Approve(ctxActor(1, "2", "agronomo"), id, "tokensecreto"); err != nil {
		t.Fatalf("primera aprobación: %v", err)
	}
	if _, err := svc.Approve(ctxActor(1, "2", "agronomo"), id, "tokensecreto"); !errors.Is(err, ErrNotPending) {
		t.Fatalf("esperaba ErrNotPending, obtuve %v", err)
	}
	if len(applier.created) != 1 {
		t.Errorf("solo la primera aprobación crea aplicación: %d", len(applier.created))
	}
}

func TestApprove_LoteNoResuelve(t *testing.T) {
	store := newFakeStore()
	// Applier SIN el lote "12" del payload: la re-validación debe fallar.
	applier := newFakeApplier(store)
	svc := newService(store, applier, &fakeAuditor{}, time.Now())

	id := seedPending(t, store, 1, "tokensecreto")
	_, err := svc.Approve(ctxActor(1, "2", "agronomo"), id, "tokensecreto")
	if err == nil {
		t.Fatal("esperaba error de re-validación")
	}
	if !strings.Contains(err.Error(), "no existe o no pertenece al tenant") {
		t.Errorf("error sin explicar la causa: %v", err)
	}
	if len(applier.created) != 0 {
		t.Error("si el lote no resuelve, NO se llama al INSERT")
	}
}

func TestApprove_PayloadManipulado(t *testing.T) {
	store := newFakeStore()
	// Payload con un campo fuera del contrato tipado: solo entra manipulando
	// la DB; la re-validación fail-closed lo rechaza.
	manipulado := json.RawMessage(`{"lote_codigo":"12","producto":"Glifosato 48%","campana":"2026/2027","dosis":3,"fecha_planificada":"2026-08-20","tenant_id":2}`)
	id, _ := store.Create(context.Background(), 1, 1, "programar_aplicacion", manipulado, hashToken("tokensecreto"), time.Now().Add(time.Hour))
	svc := newService(store, newFakeApplier(store), &fakeAuditor{}, time.Now())

	if _, err := svc.Approve(ctxActor(1, "2", "agronomo"), id, "tokensecreto"); err == nil {
		t.Fatal("esperaba error por payload con campo desconocido")
	}
}

func TestApprove_AuditorFailOpen(t *testing.T) {
	store := newFakeStore()
	applier := resolvedApplier(store)
	applier.app = domain.Aplicacion{ID: 42}
	// La auditoría FALLA: el approve debe seguir exitoso (fail-open).
	auditor := &fakeAuditor{err: errors.New("audit db caída")}
	svc := newService(store, applier, auditor, time.Now())

	id := seedPending(t, store, 1, "tokensecreto")
	if _, err := svc.Approve(ctxActor(1, "2", "agronomo"), id, "tokensecreto"); err != nil {
		t.Fatalf("un fallo de auditoría no debe tumbar el approve: %v", err)
	}
	if auditor.records != 1 {
		t.Errorf("el auditor igual fue llamado: %d", auditor.records)
	}
}

// TestConcurrentApprove_SoloUnaGana es la prueba del TOCTOU: dos approves
// concurrentes con el MISMO token válido sobre la MISMA solicitud pendiente.
// Exactamente uno gana (crea la aplicación) y el otro recibe ErrNotPending;
// al final existe exactamente UNA fila de aplicación.
func TestConcurrentApprove_SoloUnaGana(t *testing.T) {
	store := newFakeStore()
	applier := resolvedApplier(store)
	applier.app = domain.Aplicacion{ID: 42}
	svc := newService(store, applier, &fakeAuditor{}, time.Now())
	id := seedPending(t, store, 1, "tokensecreto")

	ctx := ctxActor(1, "2", "agronomo")
	const n = 2
	var wg sync.WaitGroup
	errs := make([]error, n)
	apps := make([]domain.Aplicacion, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			apps[i], errs[i] = svc.Approve(ctx, id, "tokensecreto")
		}(i)
	}
	wg.Wait()

	exitos := 0
	for i, err := range errs {
		if err == nil {
			exitos++
			if apps[i].ID != 42 {
				t.Errorf("aplicación inesperada: %+v", apps[i])
			}
			continue
		}
		// El perdedor debe recibir el error "no aprobable" (409 en HTTP).
		if !errors.Is(err, ErrNotPending) {
			t.Errorf("el perdedor debe recibir ErrNotPending, obtuvo: %v", err)
		}
	}
	if exitos != 1 {
		t.Fatalf("exactamente UNA aprobación debe ganar, ganaron %d", exitos)
	}
	if len(applier.created) != 1 {
		t.Fatalf("exactamente UNA fila de aplicación debe existir, hay %d", len(applier.created))
	}
}

// -----------------------------------------------------------------------------
// Reject y List
// -----------------------------------------------------------------------------

func TestReject_Feliz(t *testing.T) {
	store := newFakeStore()
	auditor := &fakeAuditor{}
	svc := newService(store, nil, auditor, time.Now())

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
	svc := newService(store, nil, &fakeAuditor{}, time.Now())

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
	svc := newService(store, nil, nil, time.Now())

	if _, err := svc.List(ctxActor(1, "42", "productor"), ""); err != nil {
		t.Fatalf("List: %v", err)
	}
	if store.markedExpiredTID != 1 {
		t.Errorf("MarkExpired no llamado con el tenant del ctx: %d", store.markedExpiredTID)
	}
}

func TestList_FiltraPorEstado(t *testing.T) {
	store := newFakeStore()
	svc := newService(store, nil, nil, time.Now())

	seedPending(t, store, 1, "tokensecreto")
	reqs, err := svc.List(ctxActor(1, "42", "productor"), "pendiente")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(reqs) != 1 || reqs[0].Status != StatusPending {
		t.Fatalf("filtro por estado roto: %+v", reqs)
	}
}
