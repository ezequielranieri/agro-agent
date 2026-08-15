package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/agro-agent/agro-agent/internal/store"
	"github.com/agro-agent/agro-agent/internal/tenant"
)

// ConsultarRendimientosParams: el LLM elige campaña y/o lote.
type ConsultarRendimientosParams struct {
	Campana    string `json:"campana"`
	LoteCodigo string `json:"lote_codigo"`
}

func (p ConsultarRendimientosParams) validate() error {
	if p.Campana == "" && p.LoteCodigo == "" {
		return fmt.Errorf("consultar_rendimientos: al menos un filtro (campana o lote_codigo)")
	}
	return nil
}

func ConsultarRendimientos(s store.RendimientoStore) Tool {
	return Tool{
		Name:        "consultar_rendimientos",
		Description: "Consulta rendimientos reales cosechados (tn/ha) por lote y campaña. Úsala para comparar el rendimiento entre campañas o ver el rinde histórico de un lote.",
		ParamsSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"campana":     map[string]any{"type": "string", "description": "Nombre de campaña, ej: '2024/2025'. Opcional."},
				"lote_codigo": map[string]any{"type": "string", "description": "Código del lote, ej: '12'. Opcional."},
			},
			"additionalProperties": false,
		},
		Run: func(ctx context.Context, raw json.RawMessage) (Result, error) {
			var p ConsultarRendimientosParams
			dec := json.NewDecoder(strings.NewReader(string(raw)))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&p); err != nil {
				return Result{}, fmt.Errorf("consultar_rendimientos: params inválidos: %w", err)
			}
			if err := p.validate(); err != nil {
				return Result{}, err
			}
			tid, err := tenant.FromContext(ctx)
			if err != nil {
				return Result{}, fmt.Errorf("consultar_rendimientos: %w", err)
			}
			rends, err := s.ListRendimientos(ctx, tid, store.RendimientoFilters{
				CampanaNombre: p.Campana,
				LoteCodigo:    p.LoteCodigo,
			})
			if err != nil {
				return Result{}, fmt.Errorf("consultar_rendimientos: %w", err)
			}
			return Result{Data: rends}, nil
		},
	}
}