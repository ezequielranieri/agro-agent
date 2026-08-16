// Test de integración contra Postgres REAL. Se saltea salvo que AGRO_TEST_DB
// esté definida. Ejecución:
//
//	AGRO_TEST_DB="postgres://postgres:postgres@localhost:5432/agro" go test ./internal/store/pg -v
//
// Requiere que db/schema.sql y db/seed.sql estén aplicados al contenedor.
package pg

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/agro-agent/agro-agent/internal/domain"
	"github.com/agro-agent/agro-agent/internal/store"
)

// testConn entrega un *pgxpool.Pool compartido por todos los stores del test,
// igual que el server real: es el tipo que esperan los constructores y el que
// permite correr lecturas concurrentes sin pisarse.
func testConn(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("AGRO_TEST_DB")
	if dsn == "" {
		t.Skip("AGRO_TEST_DB no definida; saltando test de integración")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("conectar a Postgres: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func mustDate(t *testing.T, s string) *time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("fecha inválida %q: %v", s, err)
	}
	return &d
}

func TestListAplicaciones_Lote12Campana2026_2027(t *testing.T) {
	s := NewAplicacionStore(testConn(t))
	apps, err := s.ListAplicaciones(context.Background(), domain.TenantID(1), store.AplicacionFilters{
		LoteCodigo:    "12",
		CampanaNombre: "2026/2027",
	})
	if err != nil {
		t.Fatalf("ListAplicaciones: %v", err)
	}
	if len(apps) != 5 {
		t.Fatalf("esperaba 5 aplicaciones del lote 12 en 2026/2027, obtuve %d", len(apps))
	}
	// Los joins deben venir poblados (el LLM formatea sobre estos campos).
	for _, a := range apps {
		if a.LoteCodigo != "12" || a.CampanaNombre != "2026/2027" || a.Producto == "" {
			t.Errorf("join sin poblar: %+v", a)
		}
	}
	// La aplicación del retraso (planificada, fecha vencida) tiene que estar.
	foundDelay := false
	for _, a := range apps {
		if a.Estado == "planificada" && a.FechaPlanificada != nil && a.FechaPlanificada.Format("2006-01-02") == "2026-08-05" {
			foundDelay = true
		}
	}
	if !foundDelay {
		t.Error("falta la aplicación del retraso (lote 12, planificada 2026-08-05)")
	}
}

func TestListAplicaciones_AislamientoTenant(t *testing.T) {
	s := NewAplicacionStore(testConn(t))
	ctx := context.Background()

	t1, err := s.ListAplicaciones(ctx, domain.TenantID(1), store.AplicacionFilters{})
	if err != nil {
		t.Fatalf("tenant 1: %v", err)
	}
	if len(t1) == 0 {
		t.Fatal("tenant 1 no debería estar vacío (hay seed)")
	}

	// El tenant 2 NO existe en el seed: debe devolver cero filas, jamás las
	// del tenant 1 por "parecerse".
	t2, err := s.ListAplicaciones(ctx, domain.TenantID(2), store.AplicacionFilters{})
	if err != nil {
		t.Fatalf("tenant 2: %v", err)
	}
	if len(t2) != 0 {
		t.Fatalf("aislamiento roto: tenant 2 devolvió %d filas del tenant 1", len(t2))
	}
}

func TestListAplicaciones_FiltraEstado(t *testing.T) {
	s := NewAplicacionStore(testConn(t))
	apps, err := s.ListAplicaciones(context.Background(), domain.TenantID(1), store.AplicacionFilters{
		Estado: "planificada",
	})
	if err != nil {
		t.Fatalf("ListAplicaciones: %v", err)
	}
	// 3 retrasos (lotes 4,7,12) + 6 fungicida + 4 insecticida = 13 planificadas.
	if len(apps) != 13 {
		t.Fatalf("esperaba 13 planificadas, obtuve %d", len(apps))
	}
}

func TestListAplicaciones_RangoDeFechas_Retraso(t *testing.T) {
	s := NewAplicacionStore(testConn(t))
	// Rango que solo captura las vencidas (el "hoy" del demo es 2026-08-14).
	apps, err := s.ListAplicaciones(context.Background(), domain.TenantID(1), store.AplicacionFilters{
		Estado: "planificada",
		Hasta:  mustDate(t, "2026-08-14"),
	})
	if err != nil {
		t.Fatalf("ListAplicaciones: %v", err)
	}
	if len(apps) != 3 { // lotes 12, 4 y 7
		t.Fatalf("esperaba 3 aplicaciones retrasadas, obtuve %d: %+v", len(apps), apps)
	}
}

func TestListAplicaciones_FiltraEjecutadasPorRango(t *testing.T) {
	s := NewAplicacionStore(testConn(t))
	// Resumen de 30 días del demo: 15-jul a 14-ago 2026 → 9 ejecutadas.
	apps, err := s.ListAplicaciones(context.Background(), domain.TenantID(1), store.AplicacionFilters{
		Estado:         "ejecutada",
		EjecutadaDesde: mustDate(t, "2026-07-15"),
		EjecutadaHasta: mustDate(t, "2026-08-14"),
	})
	if err != nil {
		t.Fatalf("ListAplicaciones: %v", err)
	}
	if len(apps) != 9 {
		t.Fatalf("esperaba 9 ejecutadas en 30 días, obtuve %d", len(apps))
	}
	for _, a := range apps {
		if a.FechaEjecucion == nil {
			t.Errorf("fila ejecutada sin fecha de ejecución: %+v", a)
		}
	}
}