package pg

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/agro-agent/agro-agent/internal/domain"
	"github.com/agro-agent/agro-agent/internal/store"
)

// TestConcurrentLecturas_CompartenPool reproduce el escenario real que
// rompía al server: el frontend dispara lotes/approvals/aplicaciones en
// paralelo y todos los stores compartían una ÚNICA *pgx.Conn. Esa conexión
// no es thread-safe: las goroutines chocaban por el canal de escritura del
// conn ("failed to deallocate cached statement(s): conn busy") y el HTTP
// devolvía 500. Con el pool, cada llamada toma una conexión libre y las
// lecturas concurrentes deben terminar sin error y con los mismos resultados.
func TestConcurrentLecturas_CompartenPool(t *testing.T) {
	// Un solo pool compartido por los tres stores, igual que cmd/api.
	pool := testConn(t)
	appStore := NewAplicacionStore(pool)
	loteStore := NewLoteStore(pool)
	rendStore := NewRendimientoStore(pool)

	const goroutines = 20
	var wg sync.WaitGroup
	errCh := make(chan error, goroutines*3)

	run := func(name string, fn func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := fn(); err != nil {
				errCh <- fmt.Errorf("%s: %w", name, err)
			}
		}()
	}

	for range goroutines {
		run("ListLotesConCampanaActual", func() error {
			lotes, err := loteStore.ListLotesConCampanaActual(context.Background(), domain.TenantID(1))
			if err != nil {
				return err
			}
			if len(lotes) != 18 {
				return fmt.Errorf("esperaba 18 lotes, obtuve %d", len(lotes))
			}
			return nil
		})
		run("ListAplicaciones", func() error {
			apps, err := appStore.ListAplicaciones(context.Background(), domain.TenantID(1), store.AplicacionFilters{})
			if err != nil {
				return err
			}
			if len(apps) == 0 {
				return fmt.Errorf("tenant 1 tiene seed, obtuve 0 aplicaciones")
			}
			return nil
		})
		run("ListRendimientos", func() error {
			rends, err := rendStore.ListRendimientos(context.Background(), domain.TenantID(1), store.RendimientoFilters{})
			if err != nil {
				return err
			}
			if len(rends) == 0 {
				return fmt.Errorf("tenant 1 tiene seed, obtuve 0 rendimientos")
			}
			return nil
		})
	}

	wg.Wait()
	close(errCh)

	failures := 0
	var firstErr error
	for err := range errCh {
		if failures == 0 {
			firstErr = err
		}
		failures++
	}
	if failures > 0 {
		t.Fatalf("lecturas concurrentes fallaron (%d de %d llamadas): %v", failures, goroutines*3, firstErr)
	}
}