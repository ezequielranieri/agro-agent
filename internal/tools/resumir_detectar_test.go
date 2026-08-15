package tools

import (
	"context"
	"testing"
	"time"

	"github.com/agro-agent/agro-agent/internal/domain"
	"github.com/agro-agent/agro-agent/internal/tenant"
)

// Reloj fijo para los tests: el demo vive el 14 de agosto de 2026.
var fixedNow = func() time.Time { return time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC) }

func TestResumirAplicaciones_AgregaPorTipo(t *testing.T) {
	apps := seedApps()
	// Sumamos dos ejecutadas con fecha de ejecución dentro del rango.
	ejec := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	apps = append(apps,
		domain.Aplicacion{ID: 10, TenantID: 1, LoteCodigo: "5", CampanaNombre: "2026/2027",
			Estado: "ejecutada", Producto: "Tebuconazol 25%", ProductoTipo: "fungicida",
			FechaEjecucion: &ejec},
		domain.Aplicacion{ID: 11, TenantID: 1, LoteCodigo: "6", CampanaNombre: "2026/2027",
			Estado: "ejecutada", Producto: "Urea granulada 46%", ProductoTipo: "fertilizante",
			FechaEjecucion: &ejec},
	)
	tool := ResumirAplicaciones(&fakeAplicacionStore{apps: apps}, fixedNow)
	ctx := tenant.WithID(context.Background(), domain.TenantID(1))

	res, err := runTool(t, tool, ctx, map[string]any{
		"desde": "2026-07-15",
		"hasta": "2026-08-14",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	sum := res.Data.(ResumenAplicaciones)
	if sum.Total != 2 {
		t.Fatalf("esperaba 2 ejecutadas en rango, obtuve %d", sum.Total)
	}
	if sum.PorTipo["fungicida"].Total != 1 || sum.PorTipo["fertilizante"].Total != 1 {
		t.Errorf("agregación por tipo incorrecta: %+v", sum.PorTipo)
	}
	if len(sum.Lotes) != 2 {
		t.Errorf("lotes esperados: 2, obtuve %v", sum.Lotes)
	}
}

func TestResumirAplicaciones_ExigeDesde(t *testing.T) {
	tool := ResumirAplicaciones(&fakeAplicacionStore{apps: seedApps()}, fixedNow)
	ctx := tenant.WithID(context.Background(), domain.TenantID(1))

	if _, err := runTool(t, tool, ctx, map[string]any{"hasta": "2026-08-14"}); err == nil {
		t.Fatal("esperaba error: 'desde' es obligatorio")
	}
}

func TestDetectarRetrasos_DevuelveVencidas(t *testing.T) {
	tool := DetectarRetrasos(&fakeAplicacionStore{apps: seedApps()}, fixedNow)
	ctx := tenant.WithID(context.Background(), domain.TenantID(1))

	res, err := runTool(t, tool, ctx, map[string]any{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	retrasos := res.Data.([]Retraso)
	// seedApps (tenant 1): planificadas el 05-08 y 08-08 de 2026; el reloj fijo
	// es el 14-08 → 2 vencidas. La planificada del tenant 2 jamás entra.
	if len(retrasos) != 2 {
		t.Fatalf("esperaba 2 retrasos, obtuve %d: %+v", len(retrasos), retrasos)
	}
	for _, r := range retrasos {
		if r.LoteCodigo == "" || r.DiasRetraso <= 0 {
			t.Errorf("retraso mal formado: %+v", r)
		}
	}
}

func TestDetectarRetrasos_AcotaPorCampana(t *testing.T) {
	tool := DetectarRetrasos(&fakeAplicacionStore{apps: seedApps()}, fixedNow)
	ctx := tenant.WithID(context.Background(), domain.TenantID(1))

	res, err := runTool(t, tool, ctx, map[string]any{"campana": "2024/2025"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// En 2024/2025 no hay planificadas en el fake → 0 retrasos.
	if got := len(res.Data.([]Retraso)); got != 0 {
		t.Fatalf("esperaba 0 retrasos en 2024/2025, obtuve %d", got)
	}
}

func TestDetectarRetrasos_SinTenantFalla(t *testing.T) {
	tool := DetectarRetrasos(&fakeAplicacionStore{apps: seedApps()}, fixedNow)
	if _, err := runTool(t, tool, context.Background(), map[string]any{}); err == nil {
		t.Fatal("esperaba error sin tenant en contexto")
	}
}