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

// ResumenAplicacionesParams: el LLM pide el rango (desde es obligatorio).
type ResumenAplicacionesParams struct {
	Desde      string `json:"desde"` // YYYY-MM-DD, obligatorio
	Hasta      string `json:"hasta"` // YYYY-MM-DD, opcional (default: hoy)
	Campana    string `json:"campana"`
	LoteCodigo string `json:"lote_codigo"`
}

func (p ResumenAplicacionesParams) validate() error {
	if p.Desde == "" {
		return fmt.Errorf("resumir_aplicaciones: 'desde' es obligatorio (YYYY-MM-DD)")
	}
	if _, err := time.Parse("2006-01-02", p.Desde); err != nil {
		return fmt.Errorf("resumir_aplicaciones: 'desde' inválida %q (formato YYYY-MM-DD)", p.Desde)
	}
	if p.Hasta != "" {
		if _, err := time.Parse("2006-01-02", p.Hasta); err != nil {
			return fmt.Errorf("resumir_aplicaciones: 'hasta' inválida %q (formato YYYY-MM-DD)", p.Hasta)
		}
	}
	return nil
}

// TipoResumen agrega las aplicaciones ejecutadas por tipo de producto.
type TipoResumen struct {
	Total int `json:"total"`
}

// ResumenAplicaciones es el resultado estructurado del resumen.
type ResumenAplicaciones struct {
	Desde  string                  `json:"desde"`
	Hasta  string                  `json:"hasta"`
	Total  int                     `json:"total"`
	PorTipo map[string]TipoResumen `json:"por_tipo"`
	Lotes  []string                `json:"lotes"`
}

// ResumirAplicaciones resume las aplicaciones EJECUTADAS en un rango de
// fechas. Recibe un reloj inyectable para testear con fechas fijas.
func ResumirAplicaciones(s store.AplicacionStore, now func() time.Time) Tool {
	return Tool{
		Name:        "resumir_aplicaciones",
		Description: "Genera un resumen de las aplicaciones ejecutadas en un rango de fechas: total, desglose por tipo de producto (herbicida/fungicida/insecticida/fertilizante) y lotes involucrados. Úsala para responder 'qué se aplicó en los últimos 30 días' o resúmenes por período." + discernimientoDatosSufijo,
		Dominio:     DominioDatos,
		ParamsSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"desde":       map[string]any{"type": "string", "description": "Fecha inicial en formato YYYY-MM-DD. OBLIGATORIA."},
				"hasta":       map[string]any{"type": "string", "description": "Fecha final en formato YYYY-MM-DD. Opcional, default hoy."},
				"campana":     map[string]any{"type": "string", "description": "Nombre de campaña para acotar. Opcional."},
				"lote_codigo": map[string]any{"type": "string", "description": "Código de lote para acotar. Opcional."},
			},
			"required":             []any{"desde"},
			"additionalProperties": false,
		},
		Run: func(ctx context.Context, raw json.RawMessage) (Result, error) {
			var p ResumenAplicacionesParams
			dec := json.NewDecoder(strings.NewReader(string(raw)))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&p); err != nil {
				return Result{}, fmt.Errorf("resumir_aplicaciones: params inválidos: %w", err)
			}
			if err := p.validate(); err != nil {
				return Result{}, err
			}

			tid, err := tenant.FromContext(ctx)
			if err != nil {
				return Result{}, fmt.Errorf("resumir_aplicaciones: %w", err)
			}

			desde, _ := time.Parse("2006-01-02", p.Desde)
			hasta := now()
			if p.Hasta != "" {
				hasta, _ = time.Parse("2006-01-02", p.Hasta)
			}

			apps, err := s.ListAplicaciones(ctx, tid, store.AplicacionFilters{
				Estado:         "ejecutada",
				EjecutadaDesde: &desde,
				EjecutadaHasta: &hasta,
				CampanaNombre:  p.Campana,
				LoteCodigo:     p.LoteCodigo,
			})
			if err != nil {
				return Result{}, fmt.Errorf("resumir_aplicaciones: %w", err)
			}

			// Agregación en memoria: a escala de cooperativa (cientos de filas)
			// es correcta y mantiene el puerto simple. Si crece, se mueve a SQL.
			resumen := ResumenAplicaciones{
				Desde:   p.Desde,
				Hasta:   hasta.Format("2006-01-02"),
				PorTipo: map[string]TipoResumen{},
			}
			loteSet := map[string]struct{}{}
			for _, a := range apps {
				resumen.Total++
				t := resumen.PorTipo[a.ProductoTipo]
				t.Total++
				resumen.PorTipo[a.ProductoTipo] = t
				loteSet[a.LoteCodigo] = struct{}{}
			}
			for codigo := range loteSet {
				resumen.Lotes = append(resumen.Lotes, codigo)
			}

			return Result{Data: resumen}, nil
		},
	}
}