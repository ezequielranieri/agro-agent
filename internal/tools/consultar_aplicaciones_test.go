package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/agro-agent/agro-agent/internal/domain"
	"github.com/agro-agent/agro-agent/internal/store"
	"github.com/agro-agent/agro-agent/internal/tenant"
)

// -----------------------------------------------------------------------------
// Fake del puerto AplicacionStore (implementación en memoria, sin Postgres).
// Los tests del contrato de tools NO dependen de la DB real.
// -----------------------------------------------------------------------------
type fakeAplicacionStore struct {
	apps []domain.Aplicacion
}

func (f *fakeAplicacionStore) ListAplicaciones(_ context.Context, tid domain.TenantID, flt store.AplicacionFilters) ([]domain.Aplicacion, error) {
	var out []domain.Aplicacion
	for _, a := range f.apps {
		if a.TenantID != tid {
			continue
		}
		if flt.LoteCodigo != "" && a.LoteCodigo != flt.LoteCodigo {
			continue
		}
		if flt.CampanaNombre != "" && a.CampanaNombre != flt.CampanaNombre {
			continue
		}
		if flt.Temporada != "" && a.CampanaTemporada != flt.Temporada {
			continue
		}
		if flt.Estado != "" && a.Estado != flt.Estado {
			continue
		}
		if flt.Desde != nil && (a.FechaPlanificada == nil || a.FechaPlanificada.Before(*flt.Desde)) {
			continue
		}
		if flt.Hasta != nil && (a.FechaPlanificada == nil || a.FechaPlanificada.After(*flt.Hasta)) {
			continue
		}
		if flt.EjecutadaDesde != nil && (a.FechaEjecucion == nil || a.FechaEjecucion.Before(*flt.EjecutadaDesde)) {
			continue
		}
		if flt.EjecutadaHasta != nil && (a.FechaEjecucion == nil || a.FechaEjecucion.After(*flt.EjecutadaHasta)) {
			continue
		}
		out = append(out, a)
	}
	return out, nil
}

func ptr(t time.Time) *time.Time { return &t }

// Datos del seed, pero con DOS tenants: el 1 (La Esperanza) y el 2 (otra
// cooperativa que también tiene un "lote 12" — el bug silencioso).
func seedApps() []domain.Aplicacion {
	app := func(id int64, tid domain.TenantID, lote, campana, temporada, estado, fecha string) domain.Aplicacion {
		var fp *time.Time
		if fecha != "" {
			t, _ := time.Parse("2006-01-02", fecha)
			fp = &t
		}
		return domain.Aplicacion{
			ID: id, TenantID: tid, LoteCodigo: lote,
			CampanaNombre: campana, CampanaTemporada: temporada,
			Estado: estado, FechaPlanificada: fp,
			Producto: "Glifosato 48%", ProductoTipo: "herbicida",
			Dosis: 3.0, UnidadDosis: "L/ha",
		}
	}
	return []domain.Aplicacion{
		app(1, 1, "12", "2026/2027", "fina", "planificada", "2026-08-05"),
		app(2, 1, "12", "2024/2025", "fina", "ejecutada", "2024-06-15"),
		app(3, 1, "4", "2026/2027", "fina", "planificada", "2026-08-08"),
		app(4, 1, "12", "2026/2027", "fina", "ejecutada", "2026-08-12"),
		// Otra cooperativa, mismo código de lote: NO debe aparecer jamás.
		app(5, 2, "12", "2026/2027", "fina", "planificada", "2026-08-01"),
	}
}

func runTool(t *testing.T, tool Tool, ctx context.Context, params any) (Result, error) {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	return tool.Run(ctx, raw)
}

func TestConsultarAplicaciones_FiltraPorLoteYCampana(t *testing.T) {
	tool := ConsultarAplicaciones(&fakeAplicacionStore{apps: seedApps()})
	ctx := tenant.WithID(context.Background(), domain.TenantID(1))

	res, err := runTool(t, tool, ctx, map[string]any{
		"lote_codigo": "12",
		"campana":     "2026/2027",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	apps := res.Data.([]domain.Aplicacion)
	if len(apps) != 2 {
		t.Fatalf("esperaba 2 aplicaciones del lote 12 en 2026/2027, obtuve %d: %+v", len(apps), apps)
	}
	for _, a := range apps {
		if a.LoteCodigo != "12" || a.CampanaNombre != "2026/2027" {
			t.Errorf("fila fuera de filtro: %+v", a)
		}
	}
}

// TestConsultarAplicaciones_AislamientoDeTenant es el test MÁS importante:
// el lote "12" existe en la cooperativa 2, pero con ctx de la 1 no puede
// aparecer. Es la defensa contra el bug silencioso de multi-tenancy.
func TestConsultarAplicaciones_AislamientoDeTenant(t *testing.T) {
	tool := ConsultarAplicaciones(&fakeAplicacionStore{apps: seedApps()})
	ctx := tenant.WithID(context.Background(), domain.TenantID(1))

	res, err := runTool(t, tool, ctx, map[string]any{"lote_codigo": "12"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	apps := res.Data.([]domain.Aplicacion)
	for _, a := range apps {
		if a.TenantID != 1 {
			t.Errorf("fuga de tenant: apareció fila de tenant %d", a.TenantID)
		}
	}
	// Sanidad: el lote 12 del tenant 2 EXISTE en los datos, el aislamiento es real.
	foundOther := false
	for _, a := range seedApps() {
		if a.TenantID == 2 && a.LoteCodigo == "12" {
			foundOther = true
		}
	}
	if !foundOther {
		t.Fatal("el test perdió el caso de control: sin lote 12 en tenant 2 no prueba nada")
	}
}

func TestConsultarAplicaciones_SinTenantEnContextoFalla(t *testing.T) {
	tool := ConsultarAplicaciones(&fakeAplicacionStore{apps: seedApps()})

	_, err := runTool(t, tool, context.Background(), map[string]any{"lote_codigo": "12"})
	if err == nil {
		t.Fatal("esperaba error: no hay TenantID en el contexto y la tool operó igual")
	}
	if !strings.Contains(err.Error(), "tenant") {
		t.Errorf("el error debería mencionar el tenant, obtuve: %v", err)
	}
}

func TestConsultarAplicaciones_ValidaParams(t *testing.T) {
	tool := ConsultarAplicaciones(&fakeAplicacionStore{apps: seedApps()})
	ctx := tenant.WithID(context.Background(), domain.TenantID(1))

	cases := []struct {
		name   string
		params map[string]any
	}{
		{"estado inválido", map[string]any{"estado": "borrada"}},
		{"temporada inválida", map[string]any{"temporada": "primavera"}},
		{"fecha inválida", map[string]any{"desde": "14-08-2026"}},
		{"param desconocido", map[string]any{"tenant_id": 999}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := runTool(t, tool, ctx, tc.params); err == nil {
				t.Errorf("esperaba error para %s", tc.name)
			}
		})
	}
}

func TestConsultarAplicaciones_FiltraPorEstado(t *testing.T) {
	tool := ConsultarAplicaciones(&fakeAplicacionStore{apps: seedApps()})
	ctx := tenant.WithID(context.Background(), domain.TenantID(1))

	res, err := runTool(t, tool, ctx, map[string]any{"estado": "planificada"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	apps := res.Data.([]domain.Aplicacion)
	if len(apps) != 2 { // lote 12 (5-ago) y lote 4 (8-ago), ambas del tenant 1
		t.Fatalf("esperaba 2 planificadas, obtuve %d: %+v", len(apps), apps)
	}
}

func TestRegistry_RegistraYExponeSchema(t *testing.T) {
	tool := ConsultarAplicaciones(&fakeAplicacionStore{apps: seedApps()})
	reg := NewRegistry(tool)

	if len(reg.Names()) != 1 || reg.Names()[0] != "consultar_aplicaciones" {
		t.Fatalf("registry incorrecto: %v", reg.Names())
	}
	got, ok := reg.Get("consultar_aplicaciones")
	if !ok {
		t.Fatal("tool no registrado")
	}
	if got.ParamsSchema["type"] != "object" {
		t.Errorf("schema debe ser object, obtuve %v", got.ParamsSchema["type"])
	}

	schemas := reg.Schemas()
	if len(schemas) != 1 || schemas[0]["name"] != "consultar_aplicaciones" {
		t.Fatalf("schemas mal formados: %v", schemas)
	}
	params, ok := schemas[0]["parameters"].(map[string]any)
	if !ok {
		t.Fatalf("faltan parameters en el schema")
	}
	if _, ok := params["properties"].(map[string]any)["lote_codigo"]; !ok {
		t.Errorf("falta lote_codigo en properties")
	}
}