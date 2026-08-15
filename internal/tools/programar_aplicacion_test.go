package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/agro-agent/agro-agent/internal/approval"
	"github.com/agro-agent/agro-agent/internal/domain"
	"github.com/agro-agent/agro-agent/internal/identity"
	"github.com/agro-agent/agro-agent/internal/tenant"
)

// -----------------------------------------------------------------------------
// Fake mínimo del puerto approval.Store: las tools HITL solo crean/listan
// solicitudes, así que el resto de puertos del service puede ir en nil.
// -----------------------------------------------------------------------------
type fakeApprovalStore struct {
	byID   map[int64]approval.Request
	nextID int64
}

func newFakeApprovalStore() *fakeApprovalStore {
	return &fakeApprovalStore{byID: map[int64]approval.Request{}, nextID: 1}
}

func (f *fakeApprovalStore) Create(_ context.Context, tid domain.TenantID, actorID int64, action string, payload json.RawMessage, tokenHash string, expiresAt time.Time) (int64, error) {
	id := f.nextID
	f.nextID++
	f.byID[id] = approval.Request{
		ID: id, TenantID: tid, ActorUserID: actorID, Action: action,
		Payload: payload, Status: approval.StatusPending, TokenHash: tokenHash,
		ExpiresAt: expiresAt, CreatedAt: time.Now(),
	}
	return id, nil
}

func (f *fakeApprovalStore) GetByTenant(_ context.Context, tid domain.TenantID, id int64) (*approval.Request, error) {
	r, ok := f.byID[id]
	if !ok || r.TenantID != tid {
		return nil, approval.ErrNotFound
	}
	return &r, nil
}

func (f *fakeApprovalStore) ListByTenant(_ context.Context, tid domain.TenantID, status string) ([]approval.Request, error) {
	var out []approval.Request
	for _, r := range f.byID {
		if r.TenantID != tid {
			continue
		}
		if status != "" && string(r.Status) != status {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

func (f *fakeApprovalStore) MarkExpired(_ context.Context, _ domain.TenantID) (int, error) {
	return 0, nil
}

func (f *fakeApprovalStore) Decide(_ context.Context, _ domain.TenantID, _ int64, _ int64, _ approval.Status) error {
	return nil
}

// toolCtx arma el contexto con tenant y actor, como lo haría requireAuth.
func toolCtx() context.Context {
	ctx := tenant.WithID(context.Background(), domain.TenantID(1))
	return identity.WithUserRole(ctx, "42", "productor")
}

func TestProgramarAplicacion_ParamsValidos(t *testing.T) {
	store := newFakeApprovalStore()
	tool := ProgramarAplicacion(approval.New(store, nil, nil, nil, time.Hour))
	ctx := toolCtx()

	res, err := runTool(t, tool, ctx, map[string]any{
		"lote_codigo":       "12",
		"producto":          "Glifosato 48%",
		"campana":           "2026/2027",
		"dosis":             3.0,
		"fecha_planificada": "2026-08-20",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	data := res.Data.(map[string]any)
	if data["approval_id"].(int64) != 1 {
		t.Errorf("approval_id inesperado: %v", data["approval_id"])
	}
	if data["status"] != "pendiente" {
		t.Errorf("status inesperado: %v", data["status"])
	}
	token, _ := data["token"].(string)
	if len(token) != 64 {
		t.Errorf("token no es 64 hex: %q", token)
	}
	// El payload persistido llevó el default de unidad_dosis: la re-validación
	// del approve nunca depende de que el solicitante lo haya mandado.
	persisted := store.byID[1]
	var payload approval.AplicacionPayload
	if err := json.Unmarshal(persisted.Payload, &payload); err != nil {
		t.Fatalf("payload no es el contrato: %v", err)
	}
	if payload.UnidadDosis != "L/ha" {
		t.Errorf("unidad_dosis sin default: %q", payload.UnidadDosis)
	}
}

func TestProgramarAplicacion_ValidaParams(t *testing.T) {
	tool := ProgramarAplicacion(approval.New(newFakeApprovalStore(), nil, nil, nil, time.Hour))
	ctx := toolCtx()

	base := map[string]any{
		"lote_codigo":       "12",
		"producto":          "Glifosato 48%",
		"campana":           "2026/2027",
		"dosis":             3.0,
		"fecha_planificada": "2026-08-20",
	}
	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"campo desconocido", func(m map[string]any) { m["tenant_id"] = 999 }},
		{"dosis cero", func(m map[string]any) { m["dosis"] = 0.0 }},
		{"dosis negativa", func(m map[string]any) { m["dosis"] = -2.0 }},
		{"fecha inválida", func(m map[string]any) { m["fecha_planificada"] = "20-08-2026" }},
		{"lote vacío", func(m map[string]any) { m["lote_codigo"] = "" }},
		{"producto vacío", func(m map[string]any) { m["producto"] = "" }},
		{"campaña vacía", func(m map[string]any) { m["campana"] = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			params := map[string]any{}
			for k, v := range base {
				params[k] = v
			}
			tc.mutate(params)
			if _, err := runTool(t, tool, ctx, params); err == nil {
				t.Errorf("esperaba error para %s", tc.name)
			}
		})
	}
}

func TestProgramarAplicacion_RechazaEspacioEnCampoNoString(t *testing.T) {
	// Sanidad: el decode tipado rechaza tipos que no matchean (dosis como string).
	tool := ProgramarAplicacion(approval.New(newFakeApprovalStore(), nil, nil, nil, time.Hour))
	if _, err := runTool(t, tool, toolCtx(), map[string]any{
		"lote_codigo": "12", "producto": "Glifosato 48%", "campana": "2026/2027",
		"dosis": "tres", "fecha_planificada": "2026-08-20",
	}); err == nil {
		t.Fatal("esperaba error: dosis como string no matchea el contrato")
	}
}

func TestProgramarAplicacion_SinTenantFalla(t *testing.T) {
	tool := ProgramarAplicacion(approval.New(newFakeApprovalStore(), nil, nil, nil, time.Hour))
	_, err := runTool(t, tool, context.Background(), map[string]any{
		"lote_codigo": "12", "producto": "Glifosato 48%", "campana": "2026/2027",
		"dosis": 3.0, "fecha_planificada": "2026-08-20",
	})
	if err == nil || !strings.Contains(err.Error(), "tenant") {
		t.Fatalf("esperaba error de tenant, obtuve %v", err)
	}
}