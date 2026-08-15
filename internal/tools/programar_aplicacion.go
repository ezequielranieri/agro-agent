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

// ProgramarAplicacion es la tool de ESCRITURA del agente. A diferencia de las
// consultas (solo lectura), NO inserta la aplicación: crea una solicitud de
// aprobación PENDIENTE cuyo token un admin/agronomo debe presentar para que se
// re-valide el contexto y recién entonces se ejecute el INSERT. El loop del
// agente no cambia: la aprobación es asíncrona y queda registrada en la DB.
func ProgramarAplicacion(svc *approval.Service) Tool {
	return Tool{
		Name:        "programar_aplicacion",
		Description: "PROGRAMA una aplicación de insumo sobre un lote (crea la planificación). NO ejecuta: crea una solicitud de aprobación pendiente que un agrónomo o admin debe aprobar. Usala cuando el usuario pide planificar/aplicar/programar un producto en un lote. Devuelve el id de la solicitud y su token de aprobación.",
		ParamsSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"lote_codigo":       map[string]any{"type": "string", "description": "Código del lote, ej: '12'. Obligatorio."},
				"producto":          map[string]any{"type": "string", "description": "Nombre del producto/insumo, ej: 'Glifosato 48%'. Obligatorio."},
				"campana":           map[string]any{"type": "string", "description": "Nombre de campaña, ej: '2026/2027'. Obligatorio."},
				"dosis":             map[string]any{"type": "number", "exclusiveMinimum": 0, "description": "Dosis a aplicar, ej: 3. Obligatoria y mayor a 0."},
				"unidad_dosis":      map[string]any{"type": "string", "default": "L/ha", "description": "Unidad de la dosis. Opcional, default 'L/ha'."},
				"fecha_planificada": map[string]any{"type": "string", "description": "Fecha de planificación en formato YYYY-MM-DD. Obligatoria."},
				"notas":             map[string]any{"type": "string", "description": "Notas libres (motivo, condiciones). Opcional."},
			},
			"required":             []any{"lote_codigo", "producto", "campana", "dosis", "fecha_planificada"},
			"additionalProperties": false,
		},
		Run: func(ctx context.Context, raw json.RawMessage) (Result, error) {
			var p approval.AplicacionPayload
			// DisallowUnknownFields: un "tenant_id" o "token" enviado por el LLM
			// DEBE fallar (fail-closed); el payload guardado se re-valida al aprobar.
			dec := json.NewDecoder(strings.NewReader(string(raw)))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&p); err != nil {
				return Result{}, fmt.Errorf("programar_aplicacion: params inválidos: %w", err)
			}
			// El default de unidad va ANTES de validar y de marshalear: así el
			// payload persistido (que se re-valida en el approve) siempre lo trae.
			if p.UnidadDosis == "" {
				p.UnidadDosis = "L/ha"
			}
			if err := p.Validate(); err != nil {
				return Result{}, fmt.Errorf("programar_aplicacion: %w", err)
			}

			// El tenant SIEMPRE sale del contexto; CreateRequest lo re-lee y
			// falla cerrado si no está (misma regla que el resto de las tools).
			if _, err := tenant.FromContext(ctx); err != nil {
				return Result{}, fmt.Errorf("programar_aplicacion: %w", err)
			}

			payload, err := json.Marshal(p)
			if err != nil {
				return Result{}, fmt.Errorf("programar_aplicacion: %w", err)
			}
			req, err := svc.CreateRequest(ctx, "programar_aplicacion", payload)
			if err != nil {
				return Result{}, fmt.Errorf("programar_aplicacion: %w", err)
			}

			// El token solo se entrega acá (creación). La tool NO lo loguea ni
			// lo persiste: es el secreto que el humano presenta al aprobar.
			return Result{Data: map[string]any{
				"approval_id": req.ID,
				"status":      string(approval.StatusPending),
				"expires_at":  req.ExpiresAt.UTC().Format(time.RFC3339),
				"token":       req.Token,
				"mensaje":     fmt.Sprintf("Solicitud %d creada. Un agrónomo o admin debe aprobarla con el token: %s", req.ID, req.Token),
			}}, nil
		},
	}
}