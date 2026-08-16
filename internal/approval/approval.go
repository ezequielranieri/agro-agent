// Package approval es el corazón del HITL (human-in-the-loop): las acciones de
// escritura del agente NO se ejecutan directo. Una tool crea una solicitud
// PENDIENTE con un token de aprobación opaco; un humano (admin/agronomo) la
// aprueba presentando el token; AL APROBAR se re-valida el contexto (lote,
// producto, campaña y vigencia) y recién entonces se inserta la aplicación.
//
// Reglas de seguridad:
//   - El token viaja al solicitante SOLO al crear la solicitud; el store guarda
//     únicamente su hash sha256 (la DB no puede filtrar el secreto).
//   - Toda consulta multi-tenant lleva tenant_id del contexto, jamás del input.
package approval

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/agro-agent/agro-agent/internal/domain"
)

// Status modela el ciclo de vida de una solicitud. Los valores en castellano
// son los que persiste la DB (CHECK constraint) y los que devuelve la API.
type Status string

const (
	StatusPending  Status = "pendiente"
	StatusApproved Status = "aprobado"
	StatusRejected Status = "rechazado"
	StatusExecuted Status = "ejecutado"
	StatusExpired  Status = "vencido"
)

// Request es una solicitud de aprobación. Token se llena SOLO al crear (para
// devolverlo al solicitante); el store nunca lo persiste. TokenHash lo llena
// el store con el hash sha256, y es lo único que viaja a/desde la DB.
type Request struct {
	ID          int64
	TenantID    domain.TenantID
	ActorUserID int64
	Action      string
	Payload     json.RawMessage
	Status      Status
	Token       string // solo se llena al crear; jamás se persiste ni se expone luego
	TokenHash   string // sha256 hex del token (lo llena el store, nunca el token plano)
	ExpiresAt   time.Time
	CreatedAt   time.Time
	DecidedBy   *int64
	DecidedAt   *time.Time
	ExecutedAt  *time.Time
}

// Store es el puerto de persistencia de solicitudes. Actor y decisor van como
// int64 porque la DB los guarda como BIGINT; el parseo del claim (string) lo
// hace el service.
type Store interface {
	Create(ctx context.Context, tid domain.TenantID, actorID int64, action string, payload json.RawMessage, tokenHash string, expiresAt time.Time) (int64, error)
	GetByTenant(ctx context.Context, tid domain.TenantID, id int64) (*Request, error)
	ListByTenant(ctx context.Context, tid domain.TenantID, status string) ([]Request, error)
	MarkExpired(ctx context.Context, tid domain.TenantID) (int, error)
	Decide(ctx context.Context, tid domain.TenantID, id, decidedBy int64, status Status) error
}

// Applier materializa una aprobación DENTRO de UNA transacción: re-valida el
// contexto (resuelve lote/producto/campaña acotados al tenant), decide la
// solicitud con guarda condicional (solo una fila 'pendiente' puede pasar a
// 'ejecutado') e inserta la aplicación. Al estar todo en la misma transacción,
// solo el ganador de la carrera commitea: dos approves concurrentes con el
// mismo token válido NO pueden insertar una aplicación duplicada.
type Applier interface {
	Apply(ctx context.Context, tid domain.TenantID, id, decidedBy int64, p AplicacionPayload) (domain.Aplicacion, error)
}

// Auditor registra cada decisión (aprobó/rechazó). Es fail-open por diseño:
// un fallo de auditoría NUNCA debe impedir una acción de negocio ya decidida.
type Auditor interface {
	Record(ctx context.Context, tid domain.TenantID, actorID int64, action, tool string, params, result any) error
}

// AplicacionPayload es el contrato de la tool `programar_aplicacion` y, a la
// vez, lo que se re-valida al aprobar. Se guarda como payload en la DB y se
// re-parsea en el approve (fail-closed, con DisallowUnknownFields) para que un
// payload manipulado no escape a la validación de la tool.
type AplicacionPayload struct {
	LoteCodigo       string  `json:"lote_codigo"`
	Producto         string  `json:"producto"`
	Campana          string  `json:"campana"`
	Dosis            float64 `json:"dosis"`
	UnidadDosis      string  `json:"unidad_dosis"`
	FechaPlanificada string  `json:"fecha_planificada"` // YYYY-MM-DD
	Notas            string  `json:"notas"`
}

// Validate es la validación de negocio compartida entre la tool y la
// re-validación del approve. UnidadDosis admite vacío (la tool lo
// default a 'L/ha' antes de marshal; el approve re-usa el mismo default).
func (p AplicacionPayload) Validate() error {
	switch {
	case p.LoteCodigo == "":
		return errors.New("lote_codigo es obligatorio")
	case p.Producto == "":
		return errors.New("producto es obligatorio")
	case p.Campana == "":
		return errors.New("campana es obligatoria")
	case p.Dosis <= 0:
		return errors.New("dosis debe ser mayor a 0")
	case p.FechaPlanificada == "":
		return errors.New("fecha_planificada es obligatoria (YYYY-MM-DD)")
	}
	if _, err := time.Parse("2006-01-02", p.FechaPlanificada); err != nil {
		return errors.New("fecha_planificada inválida (formato YYYY-MM-DD)")
	}
	return nil
}

// Errores centinela del caso de uso. El transporte los mapea a códigos HTTP
// uniformes (409) sin filtrar el detalle interno.
var (
	ErrNotFound     = errors.New("approval: no encontrada")
	ErrInvalidToken = errors.New("approval: token inválido")
	ErrExpired      = errors.New("approval: solicitud vencida")
	ErrNotPending   = errors.New("approval: solicitud no está pendiente")
)
