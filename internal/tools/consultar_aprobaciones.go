package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/agro-agent/agro-agent/internal/approval"
	"github.com/agro-agent/agro-agent/internal/tenant"
)

// approvalView es la proyección EXTERNA de una solicitud: sin token ni hash.
// El struct domain.Request expone TokenHash (lo necesita el service para
// aprobar), pero ese campo JAMÁS debe llegar al LLM: si el agente lo repitiera
// en una respuesta, el hash quedaría en el historial de la conversación.
type approvalView struct {
	ID          int64           `json:"id"`
	Action      string          `json:"action"`
	Status      approval.Status `json:"status"`
	Payload     json.RawMessage `json:"payload"`
	ExpiresAt   time.Time       `json:"expires_at"`
	CreatedAt   time.Time       `json:"created_at"`
	ActorUserID int64           `json:"actor_user_id"`
}

// ConsultarAprobaciones permite al agente conocer el estado de las solicitudes
// del tenant (pendientes, aprobadas, rechazadas, ejecutadas, vencidas). Es de
// SOLO LECTURA: aprobar/rechazar es decisión humana por HTTP, no del agente.
func ConsultarAprobaciones(svc *approval.Service) Tool {
	return Tool{
		Name:        "consultar_aprobaciones",
		Description: "Consulta el estado de las solicitudes de aprobación (pendientes, aprobadas, rechazadas, ejecutadas, vencidas) del tenant. Úsala cuando el usuario pregunta si se aprobó/ejecutó una solicitud o quiere ver las pendientes." + discernimientoDatosSufijo,
		Dominio:     DominioDatos,
		ParamsSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"estado": map[string]any{
					"type":        "string",
					"enum":        []any{"pendiente", "aprobado", "rechazado", "ejecutado", "vencido"},
					"description": "Estado de la solicitud a filtrar. Opcional.",
				},
			},
			"additionalProperties": false,
		},
		Run: func(ctx context.Context, raw json.RawMessage) (Result, error) {
			var p struct {
				Estado string `json:"estado"`
			}
			dec := json.NewDecoder(strings.NewReader(string(raw)))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&p); err != nil {
				return Result{}, fmt.Errorf("consultar_aprobaciones: params inválidos: %w", err)
			}
			switch p.Estado {
			case "", "pendiente", "aprobado", "rechazado", "ejecutado", "vencido":
			default:
				return Result{}, fmt.Errorf("consultar_aprobaciones: estado inválido %q", p.Estado)
			}

			if _, err := tenant.FromContext(ctx); err != nil {
				return Result{}, fmt.Errorf("consultar_aprobaciones: %w", err)
			}

			reqs, err := svc.List(ctx, p.Estado)
			if err != nil {
				return Result{}, fmt.Errorf("consultar_aprobaciones: %w", err)
			}

			// Mapeo explícito a approvalView: se proyecta lo que el LLM necesita
			// y NADA más. Token y TokenHash se quedan fuera del contrato externo.
			out := make([]approvalView, 0, len(reqs))
			for _, r := range reqs {
				out = append(out, approvalView{
					ID: r.ID, Action: r.Action, Status: r.Status, Payload: r.Payload,
					ExpiresAt: r.ExpiresAt, CreatedAt: r.CreatedAt, ActorUserID: r.ActorUserID,
				})
			}
			return Result{Data: out}, nil
		},
	}
}