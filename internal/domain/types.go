// Package domain contiene las entidades del núcleo agro.
// Regla: NUNCA llevan lógica de transporte, LLM ni SQL.
package domain

import "time"

// TenantID identifica a una cooperativa. Se inyecta desde el middleware de
// autenticación en el contexto; el LLM NUNCA lo provee.
type TenantID int64

// Lote es una unidad geográfica de producción. Cuando se consulta por
// campaña/cultivo, la proyección incluye esos campos join.
type Lote struct {
	ID            int64
	TenantID      TenantID
	Codigo        string
	Nombre        string
	SuperficieHa  float64
	TipoSuelo     string
	ResponsableID int64
	// Proyección join (solo presente cuando se filtra por campaña/cultivo).
	CampanaNombre string
	Cultivo       string
}

// Rendimiento real por lote-campaña.
type Rendimiento struct {
	ID                int64
	TenantID          TenantID
	CampanaID         int64
	CampanaNombre     string
	LoteID            int64
	LoteCodigo        string
	Cultivo           string
	RendimientoReal   float64
	UnidadRendimiento string
	FechaCosecha      *time.Time
}

// Aplicacion es una aplicación de insumo sobre un lote, planificada o
// ejecutada. La proyección incluye los campos join (lote/campaña/producto)
// porque es lo que las tools devuelven al LLM.
type Aplicacion struct {
	ID               int64
	TenantID         TenantID
	LoteID           int64
	LoteCodigo       string
	CampanaID        int64
	CampanaNombre    string
	CampanaTemporada string
	Producto         string
	ProductoTipo     string
	Estado           string
	Dosis            float64
	UnidadDosis      string
	FechaPlanificada *time.Time
	FechaEjecucion   *time.Time
	Notas            string
}