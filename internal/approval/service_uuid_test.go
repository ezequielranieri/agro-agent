package approval

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/agro-agent/agro-agent/internal/domain"
)

// -----------------------------------------------------------------------------
// Compatibilidad agro-iam en el actor: el claim sub puede ser un UUID (agro-iam)
// y el service lo traduce al actor interno vía el puerto UserResolver, SIEMPRE
// acotado al tenant del request. Con sub entero (demo mktoken) nada cambia.
// -----------------------------------------------------------------------------

const demoUserUUID = "22222222-2222-4222-8222-222222222222"

// fakeUserResolver implementa el puerto UserResolver en memoria, keyed por
// (tenant, uuid): replica el scope por tenant del adaptador pg.
type fakeUserResolver struct {
	users map[userKey]int64
}

type userKey struct {
	tid  domain.TenantID
	uuid string
}

func (f *fakeUserResolver) ResolveUserByUUID(_ context.Context, tid domain.TenantID, uuid string) (int64, error) {
	if id, ok := f.users[userKey{tid, uuid}]; ok {
		return id, nil
	}
	return 0, errors.New("resolver: usuario no encontrado")
}

// TestApprove_SubUUIDResuelveViaResolver: el sub UUID se resuelve a 2 (Carlos)
// y el approve corre con ese actor: el applier y el auditor reciben id 2.
func TestApprove_SubUUIDResuelveViaResolver(t *testing.T) {
	store := newFakeStore()
	applier := resolvedApplier(store)
	applier.app = domain.Aplicacion{ID: 42}
	auditor := &fakeAuditor{}
	res := &fakeUserResolver{users: map[userKey]int64{{domain.TenantID(1), demoUserUUID}: 2}}
	svc := newService(store, applier, auditor, time.Now()).SetUserResolver(res)

	id := seedPending(t, store, 1, "tokensecreto")
	ctx := ctxActor(1, demoUserUUID, "agronomo")
	app, err := svc.Approve(ctx, id, "tokensecreto")
	if err != nil {
		t.Fatalf("Approve con sub UUID: %v", err)
	}
	if app.ID != 42 {
		t.Errorf("aplicación inesperada: %+v", app)
	}
	if len(applier.calls) != 1 || applier.calls[0].decidedBy != 2 {
		t.Fatalf("el applier debe recibir el actor RESUELTO (2): %+v", applier.calls)
	}
	if auditor.records != 1 {
		t.Errorf("auditor no llamado: %d", auditor.records)
	}
}

// TestApprove_SubUUIDAcotadoAlTenant: el user 2 del tenant 2 NO debe resolver
// bajo el tenant 1 (defensa cross-tenant del actor).
func TestApprove_SubUUIDAcotadoAlTenant(t *testing.T) {
	store := newFakeStore()
	applier := resolvedApplier(store)
	res := &fakeUserResolver{users: map[userKey]int64{{domain.TenantID(2), demoUserUUID}: 2}}
	svc := newService(store, applier, &fakeAuditor{}, time.Now()).SetUserResolver(res)

	id := seedPending(t, store, 1, "tokensecreto")
	ctx := ctxActor(1, demoUserUUID, "agronomo")
	if _, err := svc.Approve(ctx, id, "tokensecreto"); err == nil {
		t.Fatal("esperaba error: el usuario del tenant 2 no debe resolver en el tenant 1")
	}
	if len(applier.created) != 0 {
		t.Error("con actor irresoluble NO se debe llamar al applier")
	}
}

// TestApprove_SubUUIDIrresolubleFalla: un sub UUID que no está en users.uuid
// de este tenant → error fail-closed (el transporte lo mapea a 500, igual que
// el ParseInt-fail previo).
func TestApprove_SubUUIDIrresolubleFalla(t *testing.T) {
	store := newFakeStore()
	applier := resolvedApplier(store)
	svc := newService(store, applier, &fakeAuditor{}, time.Now()).SetUserResolver(&fakeUserResolver{})

	id := seedPending(t, store, 1, "tokensecreto")
	ctx := ctxActor(1, "00000000-0000-4000-8000-000000000000", "agronomo")
	if _, err := svc.Approve(ctx, id, "tokensecreto"); err == nil {
		t.Fatal("esperaba error con sub UUID irresoluble")
	}
}

// TestApprove_SubUUIDSinResolverFalla: sin resolver inyectado, un sub UUID no
// se puede traducir → falla cerrado (mismo contrato que antes de la
// compatibilidad agro-iam).
func TestApprove_SubUUIDSinResolverFalla(t *testing.T) {
	store := newFakeStore()
	applier := resolvedApplier(store)
	svc := newService(store, applier, &fakeAuditor{}, time.Now())

	id := seedPending(t, store, 1, "tokensecreto")
	ctx := ctxActor(1, demoUserUUID, "agronomo")
	if _, err := svc.Approve(ctx, id, "tokensecreto"); err == nil {
		t.Fatal("esperaba error con sub UUID y sin resolver")
	}
}
