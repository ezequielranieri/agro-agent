package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/agro-agent/agro-agent/internal/store"
	"github.com/agro-agent/agro-agent/internal/tenant"
)

// ConsultarAplicacionesParams es el contrato tipado del tool. Los nombres en
// snake_case son los que ve el LLM en el tool calling (JSON).
type ConsultarAplicacionesParams struct {
	LoteCodigo string `json:"lote_codigo"`
	Campana    string `json:"campana"`
	Temporada  string `json:"temporada"`
	Estado     string `json:"estado"`
	Desde      string `json:"desde"` // YYYY-MM-DD
	Hasta      string `json:"hasta"` // YYYY-MM-DD
}

func (p ConsultarAplicacionesParams) validate() error {
	switch p.Temporada {
	case "", "fina", "gruesa":
	default:
		return fmt.Errorf("temporada inválida %q (esperado fina|gruesa)", p.Temporada)
	}
	switch p.Estado {
	case "", "planificada", "ejecutada", "cancelada":
	default:
		return fmt.Errorf("estado inválido %q (esperado planificada|ejecutada|cancelada)", p.Estado)
	}
	for _, d := range []struct{ name, val string }{{"desde", p.Desde}, {"hasta", p.Hasta}} {
		if d.val == "" {
			continue
		}
		if _, err := time.Parse("2006-01-02", d.val); err != nil {
			return fmt.Errorf("%s inválida %q (formato YYYY-MM-DD)", d.name, d.val)
		}
	}
	return nil
}

// ConsultarAplicaciones construye el tool sobre el puerto AplicacionStore.
// El handler: valida → tenant del contexto → filtra → devuelve datos reales.
func ConsultarAplicaciones(s store.AplicacionStore) Tool {
	return Tool{
		Name:        "consultar_aplicaciones",
		Description: "Consulta aplicaciones de insumos (herbicidas, fungicidas, fertilizantes) sobre lotes. Úsala para responder qué se aplicó o planificó, por lote, campaña, período o estado. Devuelve dosis, fechas y producto por aplicación." + discernimientoDatosSufijo,
		Dominio:     DominioDatos,
		ParamsSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"lote_codigo": map[string]any{"type": "string", "description": "Código del lote, ej: '12'. Opcional."},
				"campana":     map[string]any{"type": "string", "description": "Nombre de campaña, ej: '2026/2027'. Opcional."},
				"temporada":   map[string]any{"type": "string", "enum": []any{"fina", "gruesa"}, "description": "Temporada de la campaña. Opcional."},
				"estado":      map[string]any{"type": "string", "enum": []any{"planificada", "ejecutada", "cancelada"}, "description": "Estado de la aplicación. Opcional."},
				"desde":       map[string]any{"type": "string", "description": "Fecha inicial en formato YYYY-MM-DD. Opcional."},
				"hasta":       map[string]any{"type": "string", "description": "Fecha final en formato YYYY-MM-DD. Opcional."},
			},
			"additionalProperties": false,
		},
		Run: func(ctx context.Context, raw json.RawMessage) (Result, error) {
			var p ConsultarAplicacionesParams
			// DisallowUnknownFields: rechaza campos fuera del contrato (fail-closed).
			// Un "tenant_id" enviado por el LLM DEBE fallar, jamás ser ignorado.
			dec := json.NewDecoder(strings.NewReader(string(raw)))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&p); err != nil {
				return Result{}, fmt.Errorf("consultar_aplicaciones: params inválidos: %w", err)
			}
			if err := p.validate(); err != nil {
				return Result{}, err
			}

			// El tenant SIEMPRE sale del contexto, nunca del input del LLM.
			tid, err := tenant.FromContext(ctx)
			if err != nil {
				return Result{}, fmt.Errorf("consultar_aplicaciones: %w", err)
			}

			f := store.AplicacionFilters{
				LoteCodigo:    p.LoteCodigo,
				CampanaNombre: p.Campana,
				Temporada:     p.Temporada,
				Estado:        p.Estado,
			}
			if p.Desde != "" {
				d, _ := time.Parse("2006-01-02", p.Desde)
				f.Desde = &d
			}
			if p.Hasta != "" {
				d, _ := time.Parse("2006-01-02", p.Hasta)
				f.Hasta = &d
			}

			apps, err := s.ListAplicaciones(ctx, tid, f)
			if err != nil {
				return Result{}, fmt.Errorf("consultar_aplicaciones: %w", err)
			}
			return Result{Data: apps}, nil
		},
	}
}