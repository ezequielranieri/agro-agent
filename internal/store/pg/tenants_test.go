// Test de integración contra Postgres REAL (compuertado por AGRO_TEST_DB,
// igual que el resto de internal/store/pg). Verifica el mapeo UUID → id que
// habilita la compatibilidad con agro-iam:
//
//	AGRO_TEST_DB="postgres://postgres:postgres@localhost:5432/agro" go test ./internal/store/pg -v
//
// Requiere db/schema.sql + db/seed.sql aplicados (o la migración 003 sobre una
// DB existente), con los UUIDs demo fijos del seed.
package pg

import (
	"context"
	"errors"
	"testing"

	"github.com/agro-agent/agro-agent/internal/domain"
	"github.com/agro-agent/agro-agent/internal/store"
)

func TestResolveTenantByUUID_Demo(t *testing.T) {
	s := NewTenantStore(testConn(t))
	tid, err := s.ResolveTenantByUUID(context.Background(), "11111111-1111-4111-8111-111111111111")
	if err != nil {
		t.Fatalf("ResolveTenantByUUID: %v", err)
	}
	if tid != 1 {
		t.Errorf("el UUID demo debe resolver al tenant 1, obtuve %d", tid)
	}
}

func TestResolveTenantByUUID_NoExiste(t *testing.T) {
	s := NewTenantStore(testConn(t))
	if _, err := s.ResolveTenantByUUID(context.Background(), "00000000-0000-4000-8000-000000000000"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("esperaba store.ErrNotFound, obtuve %v", err)
	}
}

func TestResolveUserByUUID_Demo(t *testing.T) {
	s := NewTenantStore(testConn(t))
	id, err := s.ResolveUserByUUID(context.Background(), domain.TenantID(1), "22222222-2222-4222-8222-222222222222")
	if err != nil {
		t.Fatalf("ResolveUserByUUID: %v", err)
	}
	if id != 2 {
		t.Errorf("el UUID demo debe resolver al user 2, obtuve %d", id)
	}
}

// TestResolveUserByUUID_AcotadoAlTenant: el user 2 vive en el tenant 1; bajo
// el tenant 2 la misma UUID NO debe resolver (scope de tenant obligatorio).
func TestResolveUserByUUID_AcotadoAlTenant(t *testing.T) {
	s := NewTenantStore(testConn(t))
	if _, err := s.ResolveUserByUUID(context.Background(), domain.TenantID(2), "22222222-2222-4222-8222-222222222222"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("esperaba store.ErrNotFound bajo otro tenant, obtuve %v", err)
	}
}
