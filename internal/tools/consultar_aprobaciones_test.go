package tools

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/agro-agent/agro-agent/internal/approval"
	"github.com/agro-agent/agro-agent/internal/domain"
)

func TestConsultarAprobaciones_SinFiltroYConFiltro(t *testing.T) {
	store := newFakeApprovalStore()
	// Dos solicitudes del tenant 1: una pendiente, una rechazada. El token_hash
	// se siembra NO vacío: si el JSON de salida lo filtrara, el test lo atrapa.
	store.Create(nil, domain.TenantID(1), 1, "programar_aplicacion",
		json.RawMessage(`{"lote_codigo":"12"}`), "hash-pendiente", time.Now())
	store.Create(nil, domain.TenantID(1), 1, "programar_aplicacion",
		json.RawMessage(`{"lote_codigo":"4"}`), "hash-rechazada", time.Now())
	rejected := store.byID[2]
	rejected.Status = approval.StatusRejected
	store.byID[2] = rejected

	svc := approval.New(store, nil, nil, nil, time.Hour)
	tool := ConsultarAprobaciones(svc)

	// Sin filtro: las dos.
	res, err := runTool(t, tool, toolCtx(), map[string]any{})
	if err != nil {
		t.Fatalf("Run sin filtro: %v", err)
	}
	if views, ok := res.Data.([]approvalView); !ok || len(views) != 2 {
		t.Fatalf("sin filtro esperaba 2, obtuve %+v", res.Data)
	}

	// Con filtro por estado: solo la rechazada.
	res, err = runTool(t, tool, toolCtx(), map[string]any{"estado": "rechazado"})
	if err != nil {
		t.Fatalf("Run con filtro: %v", err)
	}
	views := res.Data.([]approvalView)
	if len(views) != 1 || views[0].Status != approval.StatusRejected {
		t.Fatalf("filtro por estado roto: %+v", views)
	}
}

func TestConsultarAprobaciones_EstadoInvalido(t *testing.T) {
	tool := ConsultarAprobaciones(approval.New(newFakeApprovalStore(), nil, nil, nil, time.Hour))
	if _, err := runTool(t, tool, toolCtx(), map[string]any{"estado": "borrada"}); err == nil {
		t.Fatal("esperaba error para estado fuera del enum")
	}
	if _, err := runTool(t, tool, toolCtx(), map[string]any{"tenant_id": 1}); err == nil {
		t.Fatal("esperaba error para campo desconocido")
	}
}

// TestConsultarAprobaciones_NoFiltraToken: el JSON que ve el LLM NUNCA debe
// contener "token" ni "token_hash" (el hash, si el agente lo repitiera, quedaría
// en el historial de la conversación). Es la proyección explícita a approvalView.
func TestConsultarAprobaciones_NoFiltraToken(t *testing.T) {
	store := newFakeApprovalStore()
	store.Create(nil, domain.TenantID(1), 1, "programar_aplicacion",
		json.RawMessage(`{"lote_codigo":"12"}`), "hash-secreto-de-test", time.Now())

	tool := ConsultarAprobaciones(approval.New(store, nil, nil, nil, time.Hour))
	res, err := runTool(t, tool, toolCtx(), map[string]any{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	raw, err := json.Marshal(res.Data)
	if err != nil {
		t.Fatalf("marshal salida: %v", err)
	}
	out := string(raw)
	if strings.Contains(out, "token") || strings.Contains(out, "token_hash") {
		t.Fatalf("el JSON de salida filtra el secreto: %s", out)
	}
}