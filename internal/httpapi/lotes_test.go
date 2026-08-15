package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agro-agent/agro-agent/internal/agent"
	"github.com/agro-agent/agro-agent/internal/auth"
	"github.com/agro-agent/agro-agent/internal/domain"
	"github.com/agro-agent/agro-agent/internal/httpapi"
	"github.com/agro-agent/agro-agent/internal/store"
	"github.com/agro-agent/agro-agent/internal/tools"
)

// fakeLoteStore implementa el puerto store.LoteStore sin tocar la DB. Aparte
// de devolver lo sembrado (o el error inyectado), captura el tenant que
// recibió para poder verificar el aislamiento del request.
type fakeLoteStore struct {
	lotes     []domain.Lote
	err       error
	gotTenant domain.TenantID
}

func (f *fakeLoteStore) ListLotes(_ context.Context, _ domain.TenantID, _ store.LoteFilters) ([]domain.Lote, error) {
	return nil, nil
}

func (f *fakeLoteStore) ListLotesConCampanaActual(_ context.Context, tid domain.TenantID) ([]domain.Lote, error) {
	f.gotTenant = tid
	if f.err != nil {
		return nil, f.err
	}
	var out []domain.Lote
	for _, l := range f.lotes {
		if l.TenantID == tid {
			out = append(out, l)
		}
	}
	return out, nil
}

// newLotesServer arma el server con el store fake inyectado. approvals va en
// nil: el endpoint de lotes no toca el servicio HITL.
func newLotesServer(loteStore *fakeLoteStore) http.Handler {
	verifier, err := auth.NewVerifier("secret")
	if err != nil {
		panic(err)
	}
	ag := agent.New(&captureProvider{}, tools.NewRegistry(), agent.Options{})
	return httpapi.New(ag, verifier, nil, loteStore, &fakeAplicacionStore{}).Handler()
}

func doLotes(t *testing.T, h http.Handler, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/lotes", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestListLotes_OK(t *testing.T) {
	store := &fakeLoteStore{lotes: []domain.Lote{
		{ID: 1, TenantID: 1, Codigo: "1", Nombre: "El Rincón", SuperficieHa: 48.5, TipoSuelo: "franco-arcilloso", CampanaNombre: "2026/2027", Cultivo: "trigo"},
		// Lote de OTRA cooperativa: el aislamiento por tenant lo deja afuera.
		{ID: 9, TenantID: 2, Codigo: "1", Nombre: "Lote ajeno", SuperficieHa: 10, CampanaNombre: "2026/2027", Cultivo: "soja"},
	}}
	h := newLotesServer(store)
	token := signTestToken(t, "secret", "1", "productor")
	w := doLotes(t, h, token)

	if w.Code != http.StatusOK {
		t.Fatalf("esperaba 200, obtuve %d: %s", w.Code, w.Body.String())
	}
	// El tenant del claim viajó hasta el store (aislamiento end-to-end).
	if store.gotTenant != 1 {
		t.Errorf("el store recibió tenant %d, esperaba 1", store.gotTenant)
	}
	var resp struct {
		Lotes []struct {
			ID           int64   `json:"id"`
			Codigo       string  `json:"codigo"`
			Nombre       string  `json:"nombre"`
			SuperficieHa float64 `json:"superficie_ha"`
			TipoSuelo    string  `json:"tipo_suelo"`
			Campana      string  `json:"campana"`
			Cultivo      string  `json:"cultivo"`
		} `json:"lotes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("respuesta no es JSON: %v", err)
	}
	if len(resp.Lotes) != 1 {
		t.Fatalf("esperaba 1 lote (aislamiento de tenant), obtuve %d", len(resp.Lotes))
	}
	l := resp.Lotes[0]
	if l.ID != 1 || l.Codigo != "1" || l.Nombre != "El Rincón" || l.SuperficieHa != 48.5 ||
		l.TipoSuelo != "franco-arcilloso" || l.Campana != "2026/2027" || l.Cultivo != "trigo" {
		t.Errorf("proyección inesperada: %+v", l)
	}
	// El contrato NO expone tenant_id ni responsable_id.
	if strings.Contains(w.Body.String(), "tenant_id") || strings.Contains(w.Body.String(), "responsable") {
		t.Errorf("se filtra un campo interno: %s", w.Body.String())
	}
}

func TestListLotes_Vacio(t *testing.T) {
	h := newLotesServer(&fakeLoteStore{})
	token := signTestToken(t, "secret", "1", "admin")
	w := doLotes(t, h, token)

	if w.Code != http.StatusOK {
		t.Fatalf("esperaba 200, obtuve %d: %s", w.Code, w.Body.String())
	}
	// Contrato: array vacío ([]), nunca null.
	if !strings.Contains(w.Body.String(), `"lotes":[]`) {
		t.Errorf("esperaba lotes vacío, obtuve: %s", w.Body.String())
	}
}

func TestListLotes_ErrorStore(t *testing.T) {
	h := newLotesServer(&fakeLoteStore{err: errors.New("db caída")})
	token := signTestToken(t, "secret", "1", "admin")
	w := doLotes(t, h, token)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("esperaba 500, obtuve %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"error":"internal"`) {
		t.Errorf("error no uniforme del contrato: %s", w.Body.String())
	}
}

func TestListLotes_SinToken(t *testing.T) {
	h := newLotesServer(&fakeLoteStore{})
	w := doLotes(t, h, "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("esperaba 401, obtuve %d", w.Code)
	}
}
