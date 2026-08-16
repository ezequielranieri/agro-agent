package httpapi

import (
	"math"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
)

// rateLimiter es un token bucket por IP, en memoria y sin dependencias: el
// límite justo para frenar el abuso del endpoint de chat (que llama a un LLM
// pago varias veces por request) sin meter infraestructura de rate-limit
// distribuida. La capacidad de ráfaga es un minuto completo de cuota.
type rateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	rate    float64          // tokens por minuto (y capacidad de ráfaga)
	now     func() time.Time // reloj inyectable para tests
}

// bucket es el estado de una clave (IP): los tokens disponibles y el último
// consumo, para recalcular el refill por tiempo transcurrido.
type bucket struct {
	tokens float64
	last   time.Time
}

func newRateLimiter(rate float64) *rateLimiter {
	return &rateLimiter{buckets: map[string]*bucket{}, rate: rate, now: time.Now}
}

// Allow consume un token de la clave (IP) si quedan; si no, rechaza. El refill
// es proporcional al tiempo desde el último consumo, con tope en la capacidad.
func (l *rateLimiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b := l.buckets[key]
	if b == nil {
		b = &bucket{tokens: l.rate, last: now}
		l.buckets[key] = b
	}
	elapsed := now.Sub(b.last).Seconds()
	b.tokens = math.Min(l.rate, b.tokens+elapsed*(l.rate/60))
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// rateLimitFromEnv lee CHAT_RATE_LIMIT (requests/minuto). Ausente o inválido
// → 10/min (default). El límite es por IP del cliente.
func rateLimitFromEnv() float64 {
	const defaultRate = 10
	raw := os.Getenv("CHAT_RATE_LIMIT")
	if raw == "" {
		return defaultRate
	}
	n, err := strconv.ParseFloat(raw, 64)
	if err != nil || n <= 0 {
		return defaultRate
	}
	return n
}

// clientIP extrae la IP del RemoteAddr (sin el puerto).
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// rateLimit aplica el token bucket por IP al handler que envuelve (chat).
// Rechaza con 429 SIN romper el contrato SSE: el 429 es JSON plano y el
// frontend muestra el mensaje amigable que espera.
func (s *Server) rateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.limiter.Allow(clientIP(r)) {
			writeJSONErr(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		next.ServeHTTP(w, r)
	})
}
