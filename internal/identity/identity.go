// Package identity transporta el actor (user_id y role) aislado por request.
// Vivía en internal/httpapi (transporte), pero las tools del HITL necesitan
// leer quién pide/aprueba una acción de escritura SIN acoplarse al transporte:
// este paquete es el punto neutro que ambos comparten (como internal/tenant).
//
// Regla: el actor lo setea el middleware de auth, jamás el LLM ni el cliente.
package identity

import "context"

type userKey struct{}
type roleKey struct{}

// WithUserRole guarda la identidad del usuario autenticado en el contexto.
// El role puede quedar vacío (claims legacy): solo el user es obligatorio.
func WithUserRole(ctx context.Context, userID, role string) context.Context {
	ctx = context.WithValue(ctx, userKey{}, userID)
	return context.WithValue(ctx, roleKey{}, role)
}

// UserIDFrom recupera el id del usuario autenticado (vacío si no hay).
// Se usa para registrar quién creó/aprobó cada acción de escritura.
func UserIDFrom(ctx context.Context) string {
	s, _ := ctx.Value(userKey{}).(string)
	return s
}

// RoleFrom recupera el rol del usuario autenticado (vacío si no hay). Lo
// consume requireRole para autorizar las acciones de escritura (admin/agronomo).
func RoleFrom(ctx context.Context) string {
	s, _ := ctx.Value(roleKey{}).(string)
	return s
}
