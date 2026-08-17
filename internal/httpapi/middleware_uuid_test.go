package httpapi_test

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/agro-agent/agro-agent/internal/agent"
	"github.com/agro-agent/agro-agent/internal/approval"
	"github.com/agro-agent/agro-agent/internal/auth"
	"github.com/agro-agent/agro-agent/internal/domain"
	"github.com/agro-agent/agro-agent/internal/httpapi"
	"github.com/agro-agent/agro-agent/internal/store"
	"github.com/agro-agent/agro-agent/internal/tenant"
	"github.com/agro-agent/agro-agent/internal/tools"
)

// -----------------------------------------------------------------------------
// Compatibilidad agro-iam: tenant UUID (claim tenant_id), sub UUID (claim sub)
// y roles en inglés. El middleware acepta ambos vocabularios (accept-both):
// entero directo (demo mktoken) o UUID resuelto vía tenants.uuid.
// -----------------------------------------------------------------------------

const (
	demoTenantUUID = "11111111-1111-4111-8111-111111111111"
	demoUserUUID   = "22222222-2222-4222-8222-222222222222"
)

// fakeTenantStore implementa los puertos de resolución de identidad UUID:
// store.TenantStore (para el middleware HTTP) y approval.UserResolver (para
// el actor de las aprobaciones). Es una versión en memoria de tenants.uuid /
// users.uuid del seed demo.
type fakeTenantStore struct {
	tenants map[string]domain.TenantID
	users   map[tenantUserKey]int64
}

type tenantUserKey struct {
	tid  domain.TenantID
	uuid string
}

func newFakeTenantStore() *fakeTenantStore {
	return &fakeTenantStore{
		tenants: map[string]domain.TenantID{demoTenantUUID: 1},
		users:   map[tenantUserKey]int64{},
	}
}

func (f *fakeTenantStore) ResolveTenantByUUID(_ context.Context, uuid string) (domain.TenantID, error) {
	if tid, ok := f.tenants[uuid]; ok {
		return tid, nil
	}
	return 0, store.ErrNotFound
}

func (f *fakeTenantStore) ResolveUserByUUID(_ context.Context, tid domain.TenantID, uuid string) (int64, error) {
	if id, ok := f.users[tenantUserKey{tid, uuid}]; ok {
		return id, nil
	}
	return 0, store.ErrNotFound
}

// signTestTokenFull firma un JWT con sub configurable (signTestToken fija sub
// en "42"); permite probar el claim sub UUID de agro-iam.
func signTestTokenFull(t *testing.T, secret, tenantID, role, userID string) string {
	t.Helper()
	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":       userID,
		"tenant_id": tenantID,
		"role":      role,
		"iat":       now.Unix(),
		"exp":       now.Add(15 * time.Minute).Unix(),
	})
	s, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("firmar token: %v", err)
	}
	return s
}

// newServerWithResolver arma el server de chat con el resolver de tenants UUID
// inyectado (como cmd/api hace con SetTenantResolver).
func newServerWithResolver(res store.TenantStore, ag *agent.Agent) http.Handler {
	verifier, err := auth.NewVerifier("secret")
	if err != nil {
		panic(err)
	}
	return httpapi.New(ag, verifier, nil, &fakeLoteStore{}, &fakeAplicacionStore{}).SetTenantResolver(res).Handler()
}

// newApprovalsServerWithResolver arma el server de approvals con el resolver
// inyectado en AMBOS lados: middleware HTTP (tenant UUID) y service de
// aprobaciones (sub UUID vía SetUserResolver).
func newApprovalsServerWithResolver(t *testing.T, store *fakeApprovalStore, res *fakeTenantStore) http.Handler {
	t.Helper()
	svc := approval.New(store, &fakeApprovalApplier{store: store}, fakeApprovalAuditor{}, time.Hour).SetUserResolver(res)
	verifier, err := auth.NewVerifier("secret")
	if err != nil {
		t.Fatalf("verifier: %v", err)
	}
	ag := agent.New(&captureProvider{}, tools.NewRegistry(), agent.Options{})
	return httpapi.New(ag, verifier, svc, &fakeLoteStore{}, &fakeAplicacionStore{}).SetTenantResolver(res).Handler()
}

// TestChat_TenantIntSigueFuncionando es la regresión del camino demo: con el
// resolver PRESENTE, un tenant_id entero sigue pasando directo (ParseInt gana
// y el resolver ni se consulta).
func TestChat_TenantIntSigueFuncionando(t *testing.T) {
	fake := &captureProvider{}
	ag := agent.New(fake, tools.NewRegistry(), agent.Options{})
	h := newServerWithResolver(newFakeTenantStore(), ag)

	w := doChat(t, h, signTestToken(t, "secret", "1", "admin"), `{"message":"hola"}`, "")
	if w.Code != http.StatusOK {
		t.Fatalf("tenant int con resolver presente: esperaba 200, obtuve %d: %s", w.Code, w.Body.String())
	}
	tid, err := tenant.FromContext(fake.gotCtx)
	if err != nil || tid != 1 {
		t.Fatalf("tenant int debe quedar como 1, obtuve %d (%v)", tid, err)
	}
}

// TestChat_TenantUUIDResuelve: el tenant UUID de agro-iam se traduce al id
// interno (1) vía el resolver; el request queda aislado en ese tenant.
func TestChat_TenantUUIDResuelve(t *testing.T) {
	fake := &captureProvider{}
	ag := agent.New(fake, tools.NewRegistry(), agent.Options{})
	h := newServerWithResolver(newFakeTenantStore(), ag)

	token := signTestTokenFull(t, "secret", demoTenantUUID, "admin", "42")
	w := doChat(t, h, token, `{"message":"hola"}`, "")
	if w.Code != http.StatusOK {
		t.Fatalf("tenant UUID: esperaba 200, obtuve %d: %s", w.Code, w.Body.String())
	}
	tid, err := tenant.FromContext(fake.gotCtx)
	if err != nil || tid != 1 {
		t.Fatalf("tenant UUID debe resolver a 1, obtuve %d (%v)", tid, err)
	}
}

// TestChat_TenantUUIDDesconocido: un tenant UUID que no existe en tenants.uuid
// es un token inválido → el MISMO 401 uniforme que un int mal formado.
func TestChat_TenantUUIDDesconocido(t *testing.T) {
	ag := agent.New(&captureProvider{}, tools.NewRegistry(), agent.Options{})
	h := newServerWithResolver(newFakeTenantStore(), ag)

	token := signTestTokenFull(t, "secret", "00000000-0000-4000-8000-000000000000", "admin", "42")
	w := doChat(t, h, token, `{"message":"hola"}`, "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("tenant UUID desconocido: esperaba 401, obtuve %d", w.Code)
	}
}

// TestChat_TenantUUIDSinResolver: server sin resolver configurado (demo sin
// agro-iam) + tenant UUID → 401 fail-closed (nunca un tenant fantasma).
func TestChat_TenantUUIDSinResolver(t *testing.T) {
	ag := agent.New(&captureProvider{}, tools.NewRegistry(), agent.Options{})
	h := newTestServer(ag, "secret")

	token := signTestTokenFull(t, "secret", demoTenantUUID, "admin", "42")
	w := doChat(t, h, token, `{"message":"hola"}`, "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("tenant UUID sin resolver: esperaba 401, obtuve %d", w.Code)
	}
}

// TestApprove_RolAgronomistNormalizado: el rol en inglés de agro-iam se
// normaliza (agronomist → agronomo) al ingestar, así requireRole lo acepta.
// El tenant UUID resuelve a 1 y el sub es entero (camino demo).
func TestApprove_RolAgronomistNormalizado(t *testing.T) {
	store := newApprovalStore()
	id := seedPendingRequest(t, store, "tokensecreto")
	h := newApprovalsServerWithResolver(t, store, newFakeTenantStore())

	token := signTestTokenFull(t, "secret", demoTenantUUID, "agronomist", "42")
	w := doApprove(t, h, token, "/api/v1/approvals/"+strconv.FormatInt(id, 10)+"/approve", `{"token":"tokensecreto"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("rol agronomist normalizado: esperaba 200, obtuve %d: %s", w.Code, w.Body.String())
	}
}

// TestApprove_RolProducerNormalizado: producer → productor, que NO está en los
// roles de escritura (admin/agronomo) → 403 uniforme.
func TestApprove_RolProducerNormalizado(t *testing.T) {
	store := newApprovalStore()
	id := seedPendingRequest(t, store, "tokensecreto")
	h := newApprovalsServerWithResolver(t, store, newFakeTenantStore())

	token := signTestTokenFull(t, "secret", demoTenantUUID, "producer", "42")
	w := doApprove(t, h, token, "/api/v1/approvals/"+strconv.FormatInt(id, 10)+"/approve", `{"token":"tokensecreto"}`)
	if w.Code != http.StatusForbidden {
		t.Fatalf("rol producer: esperaba 403, obtuve %d", w.Code)
	}
}

// TestApprove_RolAuditorProhibido: auditor no tiene equivalente local (rol
// de solo lectura) → se mapea a vacío y requireRole lo rechaza con 403.
func TestApprove_RolAuditorProhibido(t *testing.T) {
	store := newApprovalStore()
	id := seedPendingRequest(t, store, "tokensecreto")
	h := newApprovalsServerWithResolver(t, store, newFakeTenantStore())

	token := signTestTokenFull(t, "secret", demoTenantUUID, "auditor", "42")
	w := doApprove(t, h, token, "/api/v1/approvals/"+strconv.FormatInt(id, 10)+"/approve", `{"token":"tokensecreto"}`)
	if w.Code != http.StatusForbidden {
		t.Fatalf("rol auditor: esperaba 403, obtuve %d", w.Code)
	}
}

// TestApprove_SubUUIDResuelve: el sub UUID de agro-iam se traduce al actor
// interno (users.uuid → id, acotado al tenant). End-to-end: tenant UUID +
// sub UUID + rol en inglés → approve 200.
func TestApprove_SubUUIDResuelve(t *testing.T) {
	store := newApprovalStore()
	id := seedPendingRequest(t, store, "tokensecreto")
	res := newFakeTenantStore()
	res.users[tenantUserKey{domain.TenantID(1), demoUserUUID}] = 2
	h := newApprovalsServerWithResolver(t, store, res)

	token := signTestTokenFull(t, "secret", demoTenantUUID, "agronomist", demoUserUUID)
	w := doApprove(t, h, token, "/api/v1/approvals/"+strconv.FormatInt(id, 10)+"/approve", `{"token":"tokensecreto"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("sub UUID resuelto: esperaba 200, obtuve %d: %s", w.Code, w.Body.String())
	}
}

// TestApprove_SubUUIDIrresoluble: un sub UUID que no existe en users.uuid (de
// este tenant) no puede ser actor → error del service que el transporte mapea
// como hoy (500), preservando el contrato previo del ParseInt-fail.
func TestApprove_SubUUIDIrresoluble(t *testing.T) {
	store := newApprovalStore()
	id := seedPendingRequest(t, store, "tokensecreto")
	h := newApprovalsServerWithResolver(t, store, newFakeTenantStore())

	token := signTestTokenFull(t, "secret", demoTenantUUID, "agronomist", "00000000-0000-4000-8000-000000000000")
	w := doApprove(t, h, token, "/api/v1/approvals/"+strconv.FormatInt(id, 10)+"/approve", `{"token":"tokensecreto"}`)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("sub UUID irresoluble: esperaba 500 (contrato previo del actor), obtuve %d", w.Code)
	}
}
