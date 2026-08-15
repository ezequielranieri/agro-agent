// Package tenant transporta el TenantID aislado por request.
// Todo handler de tool lee el tenant del contexto; jamás del input del LLM.
package tenant

import (
	"context"
	"errors"

	"github.com/agro-agent/agro-agent/internal/domain"
)

type ctxKey struct{}

// WithID guarda el TenantID en el contexto (lo setea el middleware de auth).
func WithID(ctx context.Context, id domain.TenantID) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// FromContext recupera el TenantID. Error si no está: el handler DEBE fallar
// cerrado antes que operar sin tenant.
func FromContext(ctx context.Context) (domain.TenantID, error) {
	id, ok := ctx.Value(ctxKey{}).(domain.TenantID)
	if !ok {
		return 0, errors.New("tenant: no hay TenantID en el contexto")
	}
	return id, nil
}