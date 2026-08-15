// Package store define los PUERTOS (interfaces) de acceso a datos.
// Los adaptadores (PostgreSQL, fakes en test) los implementan.
package store

import (
	"context"
	"time"

	"github.com/agro-agent/agro-agent/internal/domain"
)

// AplicacionFilters son los parámetros que el LLM puede controlar.
// TenantID NO está acá: siempre llega por contexto.
type AplicacionFilters struct {
	LoteCodigo     string
	CampanaNombre  string
	Temporada      string
	Estado         string
	Desde          *time.Time // sobre fecha_planificada
	Hasta          *time.Time // sobre fecha_planificada
	EjecutadaDesde *time.Time // sobre fecha_ejecucion
	EjecutadaHasta *time.Time // sobre fecha_ejecucion
}

// AplicacionStore es el puerto del corazón del sistema.
type AplicacionStore interface {
	ListAplicaciones(ctx context.Context, tenantID domain.TenantID, f AplicacionFilters) ([]domain.Aplicacion, error)
}