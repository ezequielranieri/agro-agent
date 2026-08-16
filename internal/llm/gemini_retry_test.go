package llm

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"google.golang.org/genai"
)

// TestRetryableAPIError: solo 429/5xx y errores de transporte ameritan
// reintento. Un 400/404 es del cliente y NO se reintenta.
func TestRetryableAPIError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"429", genai.APIError{Code: http.StatusTooManyRequests}, true},
		{"500", genai.APIError{Code: http.StatusInternalServerError}, true},
		{"502", genai.APIError{Code: http.StatusBadGateway}, true},
		{"503", genai.APIError{Code: http.StatusServiceUnavailable}, true},
		{"504", genai.APIError{Code: http.StatusGatewayTimeout}, true},
		{"400", genai.APIError{Code: http.StatusBadRequest}, false},
		{"404", genai.APIError{Code: http.StatusNotFound}, false},
		{"transporte", errors.New("connection reset"), true},
	}
	for _, c := range cases {
		if got := retryableAPIError(c.err); got != c.want {
			t.Errorf("%s: esperaba %v, obtuve %v", c.name, c.want, got)
		}
	}
}

// TestRetryDelayOf: la espera respeta el RetryInfo del error, cae a 1s sin
// sugerencia y queda acotada a 5s.
func TestRetryDelayOf(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want time.Duration
	}{
		{"sin detalle", genai.APIError{Code: 429}, time.Second},
		{"string protobuf", genai.APIError{Code: 429, Details: []map[string]any{{"retryDelay": "2s"}}}, 2 * time.Second},
		{"objeto seconds", genai.APIError{Code: 429, Details: []map[string]any{{"retryDelay": map[string]any{"seconds": float64(3)}}}}, 3 * time.Second},
		{"objeto seconds+nanos", genai.APIError{Code: 429, Details: []map[string]any{{"retryDelay": map[string]any{"seconds": float64(1), "nanos": float64(500000000)}}}}, 1500 * time.Millisecond},
		{"cap 5s", genai.APIError{Code: 429, Details: []map[string]any{{"retryDelay": "120s"}}}, 5 * time.Second},
		{"mal formado", genai.APIError{Code: 429, Details: []map[string]any{{"retryDelay": "xyz"}}}, time.Second},
		{"sin clave retryDelay", genai.APIError{Code: 429, Details: []map[string]any{{"foo": "bar"}}}, time.Second},
	}
	for _, c := range cases {
		if got := retryDelayOf(c.err); got != c.want {
			t.Errorf("%s: esperaba %v, obtuve %v", c.name, c.want, got)
		}
	}
}
