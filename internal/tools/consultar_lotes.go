package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/agro-agent/agro-agent/internal/store"
	"github.com/agro-agent/agro-agent/internal/tenant"
)

// ConsultarLotesParams: filtros de lotes que el LLM puede controlar.
type ConsultarLotesParams struct {
	LoteCodigo string `json:"lote_codigo"`
	Campana    string `json:"campana"`
	Cultivo    string `json:"cultivo"`
}

func (p ConsultarLotesParams) validate() error {
	if p.LoteCodigo == "" && p.Campana == "" && p.Cultivo == "" {
		return fmt.Errorf("consultar_lotes: al menos un filtro (lote_codigo, campana o cultivo)")
	}
	return nil
}

func ConsultarLotes(s store.LoteStore) Tool {
	return Tool{
		Name:        "consultar_lotes",
		Description: "Consulta lotes de la cooperativa. Úsala para responder qué lotes existen, por código, campaña o cultivo (ej: todos los lotes con trigo en la campaña 2026/2027).",
		ParamsSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"lote_codigo": map[string]any{"type": "string", "description": "Código del lote, ej: '12'. Opcional."},
				"campana":     map[string]any{"type": "string", "description": "Nombre de campaña, ej: '2026/2027'. Opcional."},
				"cultivo":     map[string]any{"type": "string", "description": "Cultivo, ej: 'trigo'. Opcional."},
			},
			"additionalProperties": false,
		},
		Run: func(ctx context.Context, raw json.RawMessage) (Result, error) {
			var p ConsultarLotesParams
			dec := json.NewDecoder(strings.NewReader(string(raw)))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&p); err != nil {
				return Result{}, fmt.Errorf("consultar_lotes: params inválidos: %w", err)
			}
			if err := p.validate(); err != nil {
				return Result{}, err
			}
			tid, err := tenant.FromContext(ctx)
			if err != nil {
				return Result{}, fmt.Errorf("consultar_lotes: %w", err)
			}
			lotes, err := s.ListLotes(ctx, tid, store.LoteFilters{
				LoteCodigo:    p.LoteCodigo,
				CampanaNombre: p.Campana,
				Cultivo:       p.Cultivo,
			})
			if err != nil {
				return Result{}, fmt.Errorf("consultar_lotes: %w", err)
			}
			return Result{Data: lotes}, nil
		},
	}
}