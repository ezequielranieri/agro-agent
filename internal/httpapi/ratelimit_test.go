package httpapi

import (
	"testing"
	"time"
)

// TestRateLimiter_RafagaYCarga: permite hasta la ráfaga completa (un minuto de
// cuota) y recarga el bucket con el paso del tiempo.
func TestRateLimiter_RafagaYCarga(t *testing.T) {
	now := time.Now()
	l := newRateLimiter(3)
	l.now = func() time.Time { return now }

	key := "10.0.0.1"
	for i := 0; i < 3; i++ {
		if !l.Allow(key) {
			t.Fatalf("consumo %d debería permitirse", i+1)
		}
	}
	if l.Allow(key) {
		t.Fatal("el 4º consumo debe rechazarse (ráfaga agotada)")
	}
	// Pasa un minuto: el bucket se recarga a su capacidad (3 tokens).
	now = now.Add(time.Minute)
	for i := 0; i < 3; i++ {
		if !l.Allow(key) {
			t.Fatalf("tras la recarga el consumo %d debería permitirse", i+1)
		}
	}
	if l.Allow(key) {
		t.Fatal("la recarga no debe superar la capacidad del bucket")
	}
}

// TestRateLimiter_ClavesIndependientes: cada IP tiene su propio bucket.
func TestRateLimiter_ClavesIndependientes(t *testing.T) {
	now := time.Now()
	l := newRateLimiter(1)
	l.now = func() time.Time { return now }

	if !l.Allow("10.0.0.1") {
		t.Fatal("la primera IP debería poder consumir")
	}
	if l.Allow("10.0.0.1") {
		t.Fatal("el segundo consumo de la misma IP debe rechazarse")
	}
	if !l.Allow("10.0.0.2") {
		t.Fatal("otra IP tiene su propio bucket")
	}
}

// TestRateLimiter_RefillParcial: el refill es proporcional al tiempo transcurrido
// (30s de un minuto ⇒ media cuota).
func TestRateLimiter_RefillParcial(t *testing.T) {
	now := time.Now()
	l := newRateLimiter(4)
	l.now = func() time.Time { return now }

	for i := 0; i < 4; i++ {
		if !l.Allow("10.0.0.1") {
			t.Fatalf("consumo %d debería permitirse", i+1)
		}
	}
	if l.Allow("10.0.0.1") {
		t.Fatal("la ráfaga debería estar agotada")
	}
	// 30s después se recargó la mitad (2 tokens), no la capacidad completa.
	now = now.Add(30 * time.Second)
	for i := 0; i < 2; i++ {
		if !l.Allow("10.0.0.1") {
			t.Fatalf("tras 30s el consumo %d debería permitirse", i+1)
		}
	}
	if l.Allow("10.0.0.1") {
		t.Fatal("el refill parcial no puede superar la recarga correspondiente")
	}
}
