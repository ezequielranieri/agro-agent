package httpapi_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/agro-agent/agro-agent/internal/agent"
	"github.com/agro-agent/agro-agent/internal/approval"
	"github.com/agro-agent/agro-agent/internal/auth"
	"github.com/agro-agent/agro-agent/internal/domain"
	"github.com/agro-agent/agro-agent/internal/httpapi"
	"github.com/agro-agent/agro-agent/internal/tools"
)

// -----------------------------------------------------------------------------
// Fakes de los puertos del service de approvals (mismo rol que en los tests
// del package approval, pero acá el paquete de test es httpapi_test).
// -----------------------------------------------------------------------------

type fakeApprovalStore struct {
	byID   map[int64]approval.Request
	nextID int64
}

func newApprovalStore() *fakeApprovalStore {
	return &fakeApprovalStore{byID: map[int64]approval.Request{}, nextID: 1}
}

func (f *fakeApprovalStore) Create(_ context.Context, tid domain.TenantID, actorID int64, action string, payload json.RawMessage, tokenHash string, expiresAt time.Time) (int64, error) {
	id := f.nextID
	f.nextID++
	f.byID[id] = approval.Request{
		ID: id, TenantID: tid, ActorUserID: actorID, Action: action,
		Payload: payload, Status: approval.StatusPending, TokenHash: tokenHash,
		ExpiresAt: expiresAt, CreatedAt: time.Now(),
	}
	return id, nil
}

func (f *fakeApprovalStore) GetByTenant(_ context.Context, tid domain.TenantID, id int64) (*approval.Request, error) {
	r, ok := f.byID[id]
	if !ok || r.TenantID != tid {
		return nil, approval.ErrNotFound
	}
	return &r, nil
}

func (f *fakeApprovalStore) ListByTenant(_ context.Context, tid domain.TenantID, status string) ([]approval.Request, error) {
	var out []approval.Request
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

func (f *fakeApprovalStore) MarkExpired(_ context.Context, _ domain.TenantID) (int, error) {
	return 0, nil
}
func (f *fakeApprovalStore) Decide(_ context.Context, _ domain.TenantID, _ int64, _ int64, _ approval.Status) error {
	return nil
}

type fakeApprovalApplier struct {
	store *fakeApprovalStore
}

func (a *fakeApprovalApplier) Apply(_ context.Context, _ domain.TenantID, id, _ int64, _ approval.AplicacionPayload) (domain.Aplicacion, error) {
	// Replica la guarda condicional del pg adapter para los flujos HTTP: la
	// solicitud debe seguir pendiente para materializarse.
	req, ok := a.store.byID[id]
	if !ok {
		return domain.Aplicacion{}, approval.ErrNotFound
	}
	if req.Status != approval.StatusPending {
		return domain.Aplicacion{}, approval.ErrNotPending
	}
	req.Status = approval.StatusExecuted
	a.store.byID[id] = req
	return domain.Aplicacion{ID: 77, TenantID: 1, Estado: "planificada"}, nil
}

type fakeApprovalAuditor struct{}

func (fakeApprovalAuditor) Record(context.Context, domain.TenantID, int64, string, string, any, any) error {
	return nil
}

// seedTokenHash reproduce el hash del token como lo guardaría la DB.
func seedTokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// newApprovalsServer arma el server HTTP completo (con approvals) sobre fakes.
func newApprovalsServer(t *testing.T, store *fakeApprovalStore) http.Handler {
	t.Helper()
	svc := approval.New(store, &fakeApprovalApplier{store: store}, fakeApprovalAuditor{}, time.Hour)
	verifier, err := auth.NewVerifier("secret")
	if err != nil {
		t.Fatalf("verifier: %v", err)
	}
	ag := agent.New(&captureProvider{}, tools.NewRegistry(), agent.Options{})
	return httpapi.New(ag, verifier, svc, &fakeLoteStore{}, &fakeAplicacionStore{}).Handler()
}

// seedPendingRequest siembra una solicitud pendiente con token conocido y
// expiry a futuro (el reloj real del service la considera vigente).
func seedPendingRequest(t *testing.T, store *fakeApprovalStore, token string) int64 {
	t.Helper()
	id, err := store.Create(context.Background(), domain.TenantID(1), 1, "programar_aplicacion",
		json.RawMessage(`{"lote_codigo":"12","producto":"Glifosato 48%","campana":"2026/2027","dosis":3,"unidad_dosis":"L/ha","fecha_planificada":"2026-08-20"}`),
		seedTokenHash(token), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	return id
}

func doApprove(t *testing.T, h http.Handler, token, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func doList(t *testing.T, h http.Handler, token, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestListApprovals_SinToken(t *testing.T) {
	h := newApprovalsServer(t, newApprovalStore())
	w := doList(t, h, "", "/api/v1/approvals")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("esperaba 401, obtuve %d", w.Code)
	}
}

func TestListApprovals_ProductorPuedeVer(t *testing.T) {
	store := newApprovalStore()
	seedPendingRequest(t, store, "tokensecreto")
	h := newApprovalsServer(t, store)

	token := signTestToken(t, "secret", "1", "productor")
	w := doList(t, h, token, "/api/v1/approvals")
	if w.Code != http.StatusOK {
		t.Fatalf("esperaba 200, obtuve %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"approvals"`) {
		t.Errorf("falta el array approvals: %s", w.Body.String())
	}
	// La proyección HTTP jamás filtra el hash.
	if strings.Contains(w.Body.String(), "token") {
		t.Errorf("el listado filtra el token/hash: %s", w.Body.String())
	}
}

func TestApprove_ProductorProhibido(t *testing.T) {
	h := newApprovalsServer(t, newApprovalStore())
	token := signTestToken(t, "secret", "1", "productor")
	w := doApprove(t, h, token, "/api/v1/approvals/1/approve", `{"token":"tokensecreto"}`)
	if w.Code != http.StatusForbidden {
		t.Fatalf("esperaba 403, obtuve %d", w.Code)
	}
}

func TestApprove_AdminConTokenCorrecto(t *testing.T) {
	store := newApprovalStore()
	id := seedPendingRequest(t, store, "tokensecreto")
	h := newApprovalsServer(t, store)

	token := signTestToken(t, "secret", "1", "admin")
	w := doApprove(t, h, token, "/api/v1/approvals/"+strconv.FormatInt(id, 10)+"/approve", `{"token":"tokensecreto"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("esperaba 200, obtuve %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"status":"ejecutado"`) || !strings.Contains(w.Body.String(), `"aplicacion_id":77`) {
		t.Errorf("respuesta inesperada: %s", w.Body.String())
	}
}

func TestApprove_TokenIncorrecto(t *testing.T) {
	store := newApprovalStore()
	id := seedPendingRequest(t, store, "tokensecreto")
	h := newApprovalsServer(t, store)

	token := signTestToken(t, "secret", "1", "admin")
	w := doApprove(t, h, token, "/api/v1/approvals/"+strconv.FormatInt(id, 10)+"/approve", `{"token":"token-mal"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("esperaba 409, obtuve %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "no aprobable") {
		t.Errorf("error no uniforme: %s", w.Body.String())
	}
}

func TestApprove_IdNoNumerico(t *testing.T) {
	h := newApprovalsServer(t, newApprovalStore())
	token := signTestToken(t, "secret", "1", "admin")
	w := doApprove(t, h, token, "/api/v1/approvals/abc/approve", `{"token":"x"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("esperaba 400, obtuve %d", w.Code)
	}
}

func TestApprove_BodyConCampoDesconocido(t *testing.T) {
	store := newApprovalStore()
	id := seedPendingRequest(t, store, "tokensecreto")
	h := newApprovalsServer(t, store)

	token := signTestToken(t, "secret", "1", "admin")
	w := doApprove(t, h, token, "/api/v1/approvals/"+strconv.FormatInt(id, 10)+"/approve", `{"token":"tokensecreto","tenant_id":1}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("esperaba 400, obtuve %d", w.Code)
	}
}

func TestApprove_AprobacionYaEjecutada(t *testing.T) {
	// Estado no pendiente → 409 uniforme (no 500).
	store := newApprovalStore()
	id := seedPendingRequest(t, store, "tokensecreto")
	req, _ := store.GetByTenant(context.Background(), domain.TenantID(1), id)
	req.Status = approval.StatusExecuted
	store.byID[id] = *req
	h := newApprovalsServer(t, store)

	token := signTestToken(t, "secret", "1", "agronomo")
	w := doApprove(t, h, token, "/api/v1/approvals/"+strconv.FormatInt(id, 10)+"/approve", `{"token":"tokensecreto"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("esperaba 409, obtuve %d", w.Code)
	}
}

// -----------------------------------------------------------------------------
// handleReject: la cobertura HTTP del rechazo (mismo contrato que approve).
// -----------------------------------------------------------------------------

func TestReject_AdminConTokenCorrecto(t *testing.T) {
	store := newApprovalStore()
	id := seedPendingRequest(t, store, "tokensecreto")
	h := newApprovalsServer(t, store)

	token := signTestToken(t, "secret", "1", "admin")
	w := doApprove(t, h, token, "/api/v1/approvals/"+strconv.FormatInt(id, 10)+"/reject", `{"token":"tokensecreto"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("esperaba 200, obtuve %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"status":"rechazado"`) {
		t.Errorf("respuesta inesperada: %s", w.Body.String())
	}
}

func TestReject_ProductorProhibido(t *testing.T) {
	h := newApprovalsServer(t, newApprovalStore())
	token := signTestToken(t, "secret", "1", "productor")
	w := doApprove(t, h, token, "/api/v1/approvals/1/reject", `{"token":"tokensecreto"}`)
	if w.Code != http.StatusForbidden {
		t.Fatalf("esperaba 403, obtuve %d", w.Code)
	}
}

func TestReject_IdNoNumerico(t *testing.T) {
	h := newApprovalsServer(t, newApprovalStore())
	token := signTestToken(t, "secret", "1", "admin")
	w := doApprove(t, h, token, "/api/v1/approvals/abc/reject", `{"token":"x"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("esperaba 400, obtuve %d", w.Code)
	}
}

func TestReject_BodyInvalido(t *testing.T) {
	store := newApprovalStore()
	id := seedPendingRequest(t, store, "tokensecreto")
	h := newApprovalsServer(t, store)
	token := signTestToken(t, "secret", "1", "admin")
	path := "/api/v1/approvals/" + strconv.FormatInt(id, 10) + "/reject"

	cases := []struct{ name, body string }{
		{"token vacío", `{"token":""}`},
		{"campo desconocido", `{"token":"tokensecreto","tenant_id":1}`},
		{"JSON malformado", `{no-es-json`},
	}
	for _, c := range cases {
		w := doApprove(t, h, token, path, c.body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: esperaba 400, obtuve %d (%s)", c.name, w.Code, w.Body.String())
		}
	}
}

func TestReject_TokenIncorrecto(t *testing.T) {
	store := newApprovalStore()
	id := seedPendingRequest(t, store, "tokensecreto")
	h := newApprovalsServer(t, store)

	token := signTestToken(t, "secret", "1", "agronomo")
	w := doApprove(t, h, token, "/api/v1/approvals/"+strconv.FormatInt(id, 10)+"/reject", `{"token":"token-mal"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("esperaba 409, obtuve %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "no aprobable") {
		t.Errorf("error no uniforme: %s", w.Body.String())
	}
}

func TestReject_YaRechazada(t *testing.T) {
	// Estado no pendiente → 409 uniforme (no 500).
	store := newApprovalStore()
	id := seedPendingRequest(t, store, "tokensecreto")
	req, _ := store.GetByTenant(context.Background(), domain.TenantID(1), id)
	req.Status = approval.StatusRejected
	store.byID[id] = *req
	h := newApprovalsServer(t, store)

	token := signTestToken(t, "secret", "1", "agronomo")
	w := doApprove(t, h, token, "/api/v1/approvals/"+strconv.FormatInt(id, 10)+"/reject", `{"token":"tokensecreto"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("esperaba 409, obtuve %d", w.Code)
	}
}

func TestReject_NotImplemented(t *testing.T) {
	// Server con approvals en nil (slice HITL desmontado): 501, no 500.
	h := newTestServer(agent.New(&captureProvider{}, tools.NewRegistry(), agent.Options{}), "secret")
	token := signTestToken(t, "secret", "1", "admin")
	w := doApprove(t, h, token, "/api/v1/approvals/1/reject", `{"token":"x"}`)
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("esperaba 501, obtuve %d", w.Code)
	}
}
