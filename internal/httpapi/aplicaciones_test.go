package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agro-agent/agro-agent/internal/agent"
	"github.com/agro-agent/agro-agent/internal/auth"
	"github.com/agro-agent/agro-agent/internal/domain"
	"github.com/agro-agent/agro-agent/internal/httpapi"
	"github.com/agro-agent/agro-agent/internal/store"
	"github.com/agro-agent/agro-agent/internal/tools"
)

// fakeAplicacionStore implementa el puerto store.AplicacionStore sin tocar la
// DB. Además de devolver lo sembrado (o el error inyectado), filtra por tenant
// y captura tenant + filtros para verificar aislamiento y propagación de query.
type fakeAplicacionStore struct {
	apps      []domain.Aplicacion
	err       error
	gotTenant domain.TenantID
	gotFiltro store.AplicacionFilters
}

func (f *fakeAplicacionStore) ListAplicaciones(_ context.Context, tid domain.TenantID, filtro store.AplicacionFilters) ([]domain.Aplicacion, error) {
	f.gotTenant = tid
	f.gotFiltro = filtro
	if f.err != nil {
		return nil, f.err
	}
	var out []domain.Aplicacion
	for _, a := range f.apps {
		if a.TenantID != tid {
			continue
		}
		if filtro.Estado != "" && a.Estado != filtro.Estado {
			continue
		}
		if filtro.CampanaNombre != "" && a.CampanaNombre != filtro.CampanaNombre {
			continue
		}
		out = append(out, a)
	}
	return out, nil
}

// newAplicacionesServer arma el server con el store fake inyectado. approvals
// va en nil: el endpoint de aplicaciones no toca el servicio HITL.
func newAplicacionesServer(aplicacionStore *fakeAplicacionStore) http.Handler {
	verifier, err := auth.NewVerifier("secret")
	if err != nil {
		panic(err)
	}
	ag := agent.New(&captureProvider{}, tools.NewRegistry(), agent.Options{})
	return httpapi.New(ag, verifier, nil, &fakeLoteStore{}, aplicacionStore).Handler()
}

func doAplicaciones(t *testing.T, h http.Handler, token, query string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/aplicaciones"+query, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// seedApps devuelve aplicaciones con fechas fijas en UTC: el contrato las
// expone en RFC3339 y los asserts las comparan como strings.
func seedApps() []domain.Aplicacion {
	plan := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	ejec := time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC)
	return []domain.Aplicacion{
		{
			ID: 1, TenantID: 1, LoteID: 1, LoteCodigo: "1",
			CampanaID: 3, CampanaNombre: "2026/2027", CampanaTemporada: "fina",
			Producto: "Glifosato 48%", ProductoTipo: "herbicida",
			Estado: "ejecutada", Dosis: 3, UnidadDosis: "L/ha",
			FechaPlanificada: &plan, FechaEjecucion: &ejec,
			Notas: "",
		},
		// Aplicación de OTRA cooperativa: el aislamiento por tenant la deja
		// afuera de cualquier respuesta.
		{
			ID: 9, TenantID: 2, LoteID: 9, LoteCodigo: "9",
			CampanaID: 3, CampanaNombre: "2026/2027", CampanaTemporada: "gruesa",
			Producto: "Atrazina", ProductoTipo: "herbicida",
			Estado: "planificada", Dosis: 2, UnidadDosis: "L/ha",
			FechaPlanificada: &plan, FechaEjecucion: nil,
			Notas: "lote ajeno",
		},
	}
}

func TestListAplicaciones_OK(t *testing.T) {
	store := &fakeAplicacionStore{apps: seedApps()}
	h := newAplicacionesServer(store)
	token := signTestToken(t, "secret", "1", "productor")
	w := doAplicaciones(t, h, token, "")

	if w.Code != http.StatusOK {
		t.Fatalf("esperaba 200, obtuve %d: %s", w.Code, w.Body.String())
	}
	// El tenant del claim viajó hasta el store (aislamiento end-to-end).
	if store.gotTenant != 1 {
		t.Errorf("el store recibió tenant %d, esperaba 1", store.gotTenant)
	}
	var resp struct {
		Aplicaciones []struct {
			ID               int64   `json:"id"`
			LoteID           int64   `json:"lote_id"`
			LoteCodigo       string  `json:"lote_codigo"`
			Campana          string  `json:"campana"`
			Temporada        string  `json:"temporada"`
			Producto         string  `json:"producto"`
			ProductoTipo     string  `json:"producto_tipo"`
			Estado           string  `json:"estado"`
			Dosis            float64 `json:"dosis"`
			UnidadDosis      string  `json:"unidad_dosis"`
			FechaPlanificada string  `json:"fecha_planificada"`
			FechaEjecucion   string  `json:"fecha_ejecucion"`
			Notas            string  `json:"notas"`
		} `json:"aplicaciones"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("respuesta no es JSON: %v", err)
	}
	if len(resp.Aplicaciones) != 1 {
		t.Fatalf("esperaba 1 aplicación (aislamiento de tenant), obtuve %d", len(resp.Aplicaciones))
	}
	a := resp.Aplicaciones[0]
	if a.ID != 1 || a.LoteID != 1 || a.LoteCodigo != "1" || a.Campana != "2026/2027" ||
		a.Temporada != "fina" || a.Producto != "Glifosato 48%" || a.ProductoTipo != "herbicida" ||
		a.Estado != "ejecutada" || a.Dosis != 3 || a.UnidadDosis != "L/ha" ||
		a.FechaPlanificada != "2026-06-15T00:00:00Z" || a.FechaEjecucion != "2026-06-18T00:00:00Z" ||
		a.Notas != "" {
		t.Errorf("proyección inesperada: %+v", a)
	}
	// El contrato NO expone tenant_id ni campana_id (ids internos de join).
	if strings.Contains(w.Body.String(), "tenant_id") || strings.Contains(w.Body.String(), "campana_id") {
		t.Errorf("se filtra un campo interno: %s", w.Body.String())
	}
}

func TestListAplicaciones_Empty(t *testing.T) {
	h := newAplicacionesServer(&fakeAplicacionStore{})
	token := signTestToken(t, "secret", "1", "admin")
	w := doAplicaciones(t, h, token, "")

	if w.Code != http.StatusOK {
		t.Fatalf("esperaba 200, obtuve %d: %s", w.Code, w.Body.String())
	}
	// Contrato: array vacío ([]), nunca null.
	if !strings.Contains(w.Body.String(), `"aplicaciones":[]`) {
		t.Errorf("esperaba aplicaciones vacío, obtuve: %s", w.Body.String())
	}
}

func TestListAplicaciones_EstadoInvalido(t *testing.T) {
	h := newAplicacionesServer(&fakeAplicacionStore{})
	token := signTestToken(t, "secret", "1", "admin")
	w := doAplicaciones(t, h, token, "?estado=pendiente")

	if w.Code != http.StatusBadRequest {
		t.Fatalf("esperaba 400, obtuve %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"error":"invalid request"`) {
		t.Errorf("error no uniforme del contrato: %s", w.Body.String())
	}
}

func TestListAplicaciones_FiltraEstadoYCampana(t *testing.T) {
	store := &fakeAplicacionStore{apps: seedApps()}
	h := newAplicacionesServer(store)
	token := signTestToken(t, "secret", "1", "admin")
	// La aplicación del tenant 1 tiene estado "ejecutada": el filtro la deja.
	w := doAplicaciones(t, h, token, "?estado=ejecutada&campana=2026/2027")

	if w.Code != http.StatusOK {
		t.Fatalf("esperaba 200, obtuve %d: %s", w.Code, w.Body.String())
	}
	// Los query params deben propagarse al store tal cual.
	if store.gotFiltro.Estado != "ejecutada" || store.gotFiltro.CampanaNombre != "2026/2027" {
		t.Errorf("filtros mal propagados al store: %+v", store.gotFiltro)
	}
	if !strings.Contains(w.Body.String(), `"aplicaciones":[`) {
		t.Errorf("esperaba lista con resultado, obtuve: %s", w.Body.String())
	}
}

func TestListAplicaciones_ErrorStore(t *testing.T) {
	h := newAplicacionesServer(&fakeAplicacionStore{err: errors.New("db caída")})
	token := signTestToken(t, "secret", "1", "admin")
	w := doAplicaciones(t, h, token, "")

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("esperaba 500, obtuve %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"error":"internal error"`) {
		t.Errorf("error no uniforme del contrato: %s", w.Body.String())
	}
}

func TestListAplicaciones_SinToken(t *testing.T) {
	h := newAplicacionesServer(&fakeAplicacionStore{})
	w := doAplicaciones(t, h, "", "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("esperaba 401, obtuve %d", w.Code)
	}
}
