package pg

import (
	"context"
	"testing"

	"github.com/agro-agent/agro-agent/internal/domain"
	"github.com/agro-agent/agro-agent/internal/store"
)

func TestListLotes_Campana2026_2027(t *testing.T) {
	s := NewLoteStore(testConn(t))
	lotes, err := s.ListLotes(context.Background(), domain.TenantID(1), store.LoteFilters{
		CampanaNombre: "2026/2027",
		Cultivo:       "trigo",
	})
	if err != nil {
		t.Fatalf("ListLotes: %v", err)
	}
	if len(lotes) != 12 {
		t.Fatalf("esperaba 12 lotes trigo en 2026/2027, obtuve %d", len(lotes))
	}
	for _, l := range lotes {
		if l.Cultivo != "trigo" || l.CampanaNombre != "2026/2027" {
			t.Errorf("join sin poblar: %+v", l)
		}
		if l.SuperficieHa <= 0 || l.TipoSuelo == "" {
			t.Errorf("datos del lote incompletos: %+v", l)
		}
	}
}

func TestListLotes_SinJoinNoDuplica(t *testing.T) {
	s := NewLoteStore(testConn(t))
	lotes, err := s.ListLotes(context.Background(), domain.TenantID(1), store.LoteFilters{})
	if err != nil {
		t.Fatalf("ListLotes: %v", err)
	}
	// 18 lotes, aunque varios participan en múltiples campañas: el query sin
	// join NO puede duplicar.
	if len(lotes) != 18 {
		t.Fatalf("esperaba 18 lotes sin duplicar, obtuve %d", len(lotes))
	}
}

func TestListLotesConCampanaActual_CadaLoteUnaVez(t *testing.T) {
	s := NewLoteStore(testConn(t))
	lotes, err := s.ListLotesConCampanaActual(context.Background(), domain.TenantID(1))
	if err != nil {
		t.Fatalf("ListLotesConCampanaActual: %v", err)
	}
	// 18 lotes, aunque varios participan en múltiples campañas: el LATERAL por
	// max(campana_id) elige UNA campaña por lote sin duplicar.
	if len(lotes) != 18 {
		t.Fatalf("esperaba 18 lotes sin duplicar, obtuve %d", len(lotes))
	}
	for _, l := range lotes {
		if l.CampanaNombre == "" || l.Cultivo == "" {
			t.Errorf("lote sin campaña/cultivo actual: %+v", l)
		}
	}
	// ORDER BY l.codigo: el primero es el lote 1, cuya campaña de mayor id es
	// la 3 (2026/2027, trigo) — la actual del seed.
	first := lotes[0]
	if first.Codigo != "1" || first.CampanaNombre != "2026/2027" || first.Cultivo != "trigo" {
		t.Errorf("lote 1: campaña actual inesperada: %+v", first)
	}
}

func TestListLotesConCampanaActual_AislamientoTenant(t *testing.T) {
	s := NewLoteStore(testConn(t))
	lotes, err := s.ListLotesConCampanaActual(context.Background(), domain.TenantID(2))
	if err != nil {
		t.Fatalf("ListLotesConCampanaActual: %v", err)
	}
	if len(lotes) != 0 {
		t.Fatalf("tenant 2 sin datos, obtuvo %d filas", len(lotes))
	}
}

func TestListRendimientos_Lote12ComparaCampanas(t *testing.T) {
	s := NewRendimientoStore(testConn(t))
	rends, err := s.ListRendimientos(context.Background(), domain.TenantID(1), store.RendimientoFilters{
		LoteCodigo: "12",
	})
	if err != nil {
		t.Fatalf("ListRendimientos: %v", err)
	}
	// Lote 12: trigo 2024/2025 (3.1) y soja 2024/2025 gruesa (3.2).
	if len(rends) != 2 {
		t.Fatalf("esperaba 2 rendimientos del lote 12, obtuve %d", len(rends))
	}
	for _, r := range rends {
		if r.LoteCodigo != "12" || r.RendimientoReal <= 0 {
			t.Errorf("rendimiento mal formado: %+v", r)
		}
	}
}

func TestListRendimientos_AislamientoTenant(t *testing.T) {
	s := NewRendimientoStore(testConn(t))
	rends, err := s.ListRendimientos(context.Background(), domain.TenantID(2), store.RendimientoFilters{})
	if err != nil {
		t.Fatalf("ListRendimientos: %v", err)
	}
	if len(rends) != 0 {
		t.Fatalf("tenant 2 sin datos, obtuvo %d filas", len(rends))
	}
}