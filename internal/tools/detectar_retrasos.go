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

// Retraso es una aplicación planificada con fecha vencida.
type Retraso struct {
	LoteCodigo       string `json:"lote_codigo"`
	Producto         string `json:"producto"`
	FechaPlanificada string `json:"fecha_planificada"`
	DiasRetraso      int    `json:"dias_retraso"`
	Notas            string `json:"notas"`
}

// DetectarRetrasosParams: el LLM puede acotar por campaña (opcional).
type DetectarRetrasosParams struct {
	Campana string `json:"campana"`
}

// DetectarRetrasos detecta aplicaciones planificadas cuya fecha ya venció.
// Recibe un reloj inyectable para testear con fechas fijas.
func DetectarRetrasos(s store.AplicacionStore, now func() time.Time) Tool {
	return Tool{
		Name:        "detectar_retrasos",
		Description: "Detecta aplicaciones planificadas cuya fecha ya venció (con cuántos días de retraso). Úsala para responder '¿hay algún lote con retraso en las aplicaciones planificadas?'.",
		ParamsSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"campana": map[string]any{"type": "string", "description": "Nombre de campaña para acotar. Opcional."},
			},
			"additionalProperties": false,
		},
		Run: func(ctx context.Context, raw json.RawMessage) (Result, error) {
			var p DetectarRetrasosParams
			dec := json.NewDecoder(strings.NewReader(string(raw)))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&p); err != nil {
				return Result{}, fmt.Errorf("detectar_retrasos: params inválidos: %w", err)
			}
			tid, err := tenant.FromContext(ctx)
			if err != nil {
				return Result{}, fmt.Errorf("detectar_retrasos: %w", err)
			}

			hoy := now()
			apps, err := s.ListAplicaciones(ctx, tid, store.AplicacionFilters{
				Estado:        "planificada",
				Hasta:         &hoy, // vencidas respecto del reloj inyectado
				CampanaNombre: p.Campana,
			})
			if err != nil {
				return Result{}, fmt.Errorf("detectar_retrasos: %w", err)
			}

			retrasos := make([]Retraso, 0, len(apps))
			for _, a := range apps {
				dias := int(hoy.Sub(*a.FechaPlanificada).Hours() / 24)
				retrasos = append(retrasos, Retraso{
					LoteCodigo:       a.LoteCodigo,
					Producto:         a.Producto,
					FechaPlanificada: a.FechaPlanificada.Format("2006-01-02"),
					DiasRetraso:      dias,
					Notas:            a.Notas,
				})
			}
			return Result{Data: retrasos}, nil
		},
	}
}