package httpapi

import (
	"net/http"
	"time"

	"github.com/agro-agent/agro-agent/internal/store"
	"github.com/agro-agent/agro-agent/internal/tenant"
)

// validAplicacionEstados es el vocabulario aceptado por ?estado= de la lista.
// El string vacío es válido (filtro ausente); cualquier otro valor es un error
// de contrato del cliente, no algo que delegar al store.
var validAplicacionEstados = map[string]bool{
	"": true, "planificada": true, "ejecutada": true, "cancelada": true,
}

// aplicacionOut es la proyección HTTP de una aplicación. Como en lotes, el
// shape del API usa "campana" (no "campana_nombre") y no expone los campos
// internos (tenant_id, ids de join) que el contrato no contempla.
type aplicacionOut struct {
	ID               int64      `json:"id"`
	LoteID           int64      `json:"lote_id"`
	LoteCodigo       string     `json:"lote_codigo"`
	Campana          string     `json:"campana"`
	Temporada        string     `json:"temporada"`
	Producto         string     `json:"producto"`
	ProductoTipo     string     `json:"producto_tipo"`
	Estado           string     `json:"estado"`
	Dosis            float64    `json:"dosis"`
	UnidadDosis      string     `json:"unidad_dosis"`
	FechaPlanificada *time.Time `json:"fecha_planificada"`
	FechaEjecucion   *time.Time `json:"fecha_ejecucion"`
	Notas            string     `json:"notas"`
}

// handleListAplicaciones lista las aplicaciones del tenant, opcionalmente
// filtradas por estado y campaña. Es solo lectura, como lotes: cualquier rol
// autenticado puede consultar.
func (s *Server) handleListAplicaciones(w http.ResponseWriter, r *http.Request) {
	tid, err := tenant.FromContext(r.Context())
	if err != nil {
		// El middleware inyecta el tenant antes de llegar acá: si no está, es
		// un fallo interno de la cadena, no un error del cliente.
		s.log.Error("aplicaciones: tenant ausente en el contexto", "err", err)
		writeJSONErr(w, http.StatusInternalServerError, "internal error")
		return
	}

	q := r.URL.Query()
	estado := q.Get("estado")
	if !validAplicacionEstados[estado] {
		writeJSONErr(w, http.StatusBadRequest, "invalid request")
		return
	}

	// El filtro de campaña es un string libre (el store hace match exacto sobre
	// c.nombre); el tenant SIEMPRE sale del contexto, jamás de un query param.
	f := store.AplicacionFilters{
		CampanaNombre: q.Get("campana"),
		Estado:        estado,
	}

	apps, err := s.aplicacionStore.ListAplicaciones(r.Context(), tid, f)
	if err != nil {
		s.log.Error("aplicaciones: listar", "err", err, "tenant", tid)
		writeJSONErr(w, http.StatusInternalServerError, "internal error")
		return
	}

	// make con cap 0: si no hay aplicaciones, el JSON es [] y no null (contrato).
	out := make([]aplicacionOut, 0, len(apps))
	for _, a := range apps {
		out = append(out, aplicacionOut{
			ID: a.ID, LoteID: a.LoteID, LoteCodigo: a.LoteCodigo,
			Campana: a.CampanaNombre, Temporada: a.CampanaTemporada,
			Producto: a.Producto, ProductoTipo: a.ProductoTipo,
			Estado: a.Estado, Dosis: a.Dosis, UnidadDosis: a.UnidadDosis,
			FechaPlanificada: a.FechaPlanificada, FechaEjecucion: a.FechaEjecucion,
			Notas: a.Notas,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"aplicaciones": out})
}
