package llm

import (
	"context"
	"errors"
	"fmt"
	"net"

	"google.golang.org/genai"
)

// FallbackProvider es un wrapper del puerto Provider: intenta el primario y,
// si falla con un error transitorio (cuota agotada / proveedor caído), cae al
// secundario. Cualquier otro error del primario se devuelve tal cual: el
// fallback no enmascara bugs del primario (filosofía fail-closed).
type FallbackProvider struct {
	Primary  Provider
	Fallback Provider
}

// NewFallbackProvider arma el wrapper con primario y respaldo.
func NewFallbackProvider(primary, fallback Provider) *FallbackProvider {
	return &FallbackProvider{Primary: primary, Fallback: fallback}
}

// Chat intenta el primario y, solo ante error transitorio, cae al respaldo.
func (f *FallbackProvider) Chat(ctx context.Context, messages []Message, tools []ToolSchema) (Response, error) {
	resp, err := f.Primary.Chat(ctx, messages, tools)
	if err == nil {
		return resp, nil
	}
	// Sin respaldo configurado: nos comportamos exactamente como el primario.
	if f.Fallback == nil {
		return Response{}, err
	}
	// Error NO transitorio: falla cerrado, no enmascaramos al primario.
	if !IsTransient(err) {
		return Response{}, err
	}

	fallbackResp, fallbackErr := f.Fallback.Chat(ctx, messages, tools)
	if fallbackErr == nil {
		return fallbackResp, nil
	}
	// Ambos fallaron: devolvemos el error del respaldo (el más fresco), con
	// contexto del failover para el operador.
	return Response{}, fmt.Errorf("llm: primario y respaldo fallaron: %w", fallbackErr)
}

// IsTransient decide si un error del proveedor amerita failover al respaldo.
// Son transitorios:
//
//   - 429 (cuota/rate limit) y 5xx (fallo del proveedor), en Groq o Gemini;
//   - errores de transporte (net.Error: red caída, timeout).
//
// Todo lo demás NO es transitorio (400/404, errores de contrato, bugs
// locales) y falla cerrado: el fallback no debe enmascarar el error real del
// primario.
func IsTransient(err error) bool {
	var ge *groqError
	if errors.As(err, &ge) {
		return ge.transient()
	}
	var apiErr genai.APIError
	if errors.As(err, &apiErr) {
		return transientStatus(apiErr.Code)
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		// Error de transporte (red/timeout): transitorio por naturaleza.
		return true
	}
	return false
}
