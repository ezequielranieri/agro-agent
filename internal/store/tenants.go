package store

import (
	"context"
	"errors"

	"github.com/agro-agent/agro-agent/internal/domain"
)

// ErrNotFound marca "la identidad pedida no existe" en los puertos de
// resolución. El middleware/la capa de transporte lo mapean a 401 uniforme:
// un claim con una identidad que no existe en esta DB es un token inválido.
var ErrNotFound = errors.New("store: no encontrado")

// TenantStore es el puerto de resolución de identidad de tenants. agro-iam
// emite tenant_id como UUID; el dominio interno usa TenantID (int64). Este
// puerto traduce el claim al id interno consultando tenants.uuid.
type TenantStore interface {
	ResolveTenantByUUID(ctx context.Context, uuid string) (domain.TenantID, error)
}

// UserStore es el puerto de resolución de identidad de usuarios. El claim sub
// de agro-iam es un UUID; el actor interno (approval, audit) es int64. La
// resolución SIEMPRE está acotada al tenant: un usuario de otra cooperativa no
// debe resolver bajo este tenant.
type UserStore interface {
	ResolveUserByUUID(ctx context.Context, tid domain.TenantID, uuid string) (int64, error)
}
