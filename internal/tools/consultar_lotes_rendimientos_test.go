package tools

import (
	"context"
	"testing"

	"github.com/agro-agent/agro-agent/internal/domain"
	"github.com/agro-agent/agro-agent/internal/store"
	"github.com/agro-agent/agro-agent/internal/tenant"
)

// Fakes de los puertos nuevos (misma filosofía que fakeAplicacionStore).
type fakeLoteStore struct {
	lotes []domain.Lote
}

func (f *fakeLoteStore) ListLotes(_ context.Context, tid domain.TenantID, flt store.LoteFilters) ([]domain.Lote, error) {
	var out []domain.Lote
	for _, l := range f.lotes {
		if l.TenantID != tid {
			continue
		}
		if flt.LoteCodigo != "" && l.Codigo != flt.LoteCodigo {
			continue
		}
		if flt.CampanaNombre != "" && l.CampanaNombre != flt.CampanaNombre {
			continue
		}
		if flt.Cultivo != "" && l.Cultivo != flt.Cultivo {
			continue
		}
		out = append(out, l)
	}
	return out, nil
}

func (f *fakeLoteStore) ListLotesConCampanaActual(_ context.Context, tid domain.TenantID) ([]domain.Lote, error) {
	var out []domain.Lote
	for _, l := range f.lotes {
		if l.TenantID != tid {
			continue
		}
		out = append(out, l)
	}
	return out, nil
}

func seedLotes() []domain.Lote {
	return []domain.Lote{
		{ID: 1, TenantID: 1, Codigo: "1", Nombre: "El Rincón", SuperficieHa: 48.5, TipoSuelo: "franco-arcilloso", ResponsableID: 2, CampanaNombre: "2026/2027", Cultivo: "trigo"},
		{ID: 2, TenantID: 1, Codigo: "2", Nombre: "La Loma", SuperficieHa: 62.0, TipoSuelo: "franco", ResponsableID: 2, CampanaNombre: "2026/2027", Cultivo: "trigo"},
		{ID: 3, TenantID: 1, Codigo: "3", Nombre: "Cañada Sur", SuperficieHa: 55.2, TipoSuelo: "franco-limosos", ResponsableID: 2, CampanaNombre: "2024/2025", Cultivo: "trigo"},
		// Otra cooperativa: jamás debe aparecer con ctx del tenant 1.
		{ID: 9, TenantID: 2, Codigo: "1", Nombre: "Lote ajeno", SuperficieHa: 10.0, Cultivo: "soja", CampanaNombre: "2026/2027"},
	}
}

type fakeRendimientoStore struct {
	rends []domain.Rendimiento
}

func (f *fakeRendimientoStore) ListRendimientos(_ context.Context, tid domain.TenantID, flt store.RendimientoFilters) ([]domain.Rendimiento, error) {
	var out []domain.Rendimiento
	for _, r := range f.rends {
		if r.TenantID != tid {
			continue
		}
		if flt.CampanaNombre != "" && r.CampanaNombre != flt.CampanaNombre {
			continue
		}
		if flt.LoteCodigo != "" && r.LoteCodigo != flt.LoteCodigo {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

func seedRendimientos() []domain.Rendimiento {
	return []domain.Rendimiento{
		{ID: 1, TenantID: 1, CampanaNombre: "2024/2025", LoteCodigo: "12", Cultivo: "trigo", RendimientoReal: 3.1, UnidadRendimiento: "tn/ha"},
		{ID: 2, TenantID: 1, CampanaNombre: "2025/2026", LoteCodigo: "12", Cultivo: "trigo", RendimientoReal: 4.2, UnidadRendimiento: "tn/ha"},
		{ID: 3, TenantID: 2, CampanaNombre: "2024/2025", LoteCodigo: "12", Cultivo: "trigo", RendimientoReal: 9.9, UnidadRendimiento: "tn/ha"},
	}
}

// --- consultar_lotes ---------------------------------------------------------

func TestConsultarLotes_FiltraYCumpleAislamiento(t *testing.T) {
	tool := ConsultarLotes(&fakeLoteStore{lotes: seedLotes()})
	ctx := tenant.WithID(context.Background(), domain.TenantID(1))

	res, err := runTool(t, tool, ctx, map[string]any{"campana": "2026/2027", "cultivo": "trigo"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	lotes := res.Data.([]domain.Lote)
	if len(lotes) != 2 {
		t.Fatalf("esperaba 2 lotes en 2026/2027, obtuve %d", len(lotes))
	}
	for _, l := range lotes {
		if l.TenantID != 1 {
			t.Errorf("fuga de tenant: %+v", l)
		}
	}
}

func TestConsultarLotes_RequiereFiltro(t *testing.T) {
	tool := ConsultarLotes(&fakeLoteStore{lotes: seedLotes()})
	ctx := tenant.WithID(context.Background(), domain.TenantID(1))

	if _, err := runTool(t, tool, ctx, map[string]any{}); err == nil {
		t.Fatal("esperaba error: sin filtros la consulta sería demasiado amplia")
	}
}

func TestConsultarLotes_SinTenantFalla(t *testing.T) {
	tool := ConsultarLotes(&fakeLoteStore{lotes: seedLotes()})
	if _, err := runTool(t, tool, context.Background(), map[string]any{"lote_codigo": "1"}); err == nil {
		t.Fatal("esperaba error sin tenant en contexto")
	}
}

// --- consultar_rendimientos -------------------------------------------------

func TestConsultarRendimientos_ComparaCampanas(t *testing.T) {
	tool := ConsultarRendimientos(&fakeRendimientoStore{rends: seedRendimientos()})
	ctx := tenant.WithID(context.Background(), domain.TenantID(1))

	res, err := runTool(t, tool, ctx, map[string]any{"lote_codigo": "12"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	rends := res.Data.([]domain.Rendimiento)
	if len(rends) != 2 { // 2024/2025 y 2025/2026 del tenant 1
		t.Fatalf("esperaba 2 rendimientos, obtuve %d: %+v", len(rends), rends)
	}
	// El rendimiento 9.9 del tenant 2 NO puede estar.
	for _, r := range rends {
		if r.RendimientoReal == 9.9 {
			t.Fatal("fuga de tenant: rendimiento del tenant 2 en respuesta")
		}
	}
}

func TestConsultarRendimientos_RequiereFiltro(t *testing.T) {
	tool := ConsultarRendimientos(&fakeRendimientoStore{rends: seedRendimientos()})
	ctx := tenant.WithID(context.Background(), domain.TenantID(1))
	if _, err := runTool(t, tool, ctx, map[string]any{}); err == nil {
		t.Fatal("esperaba error sin filtros")
	}
}