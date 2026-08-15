package httpapi

import (
	"net/http"

	"github.com/agro-agent/agro-agent/internal/tenant"
)

// loteOut es la proyección HTTP de un lote. El contrato del endpoint usa
// "campana" (no "campana_nombre" como el domain): el shape de la API no tiene
// por qué replicar los nombres internos.
type loteOut struct {
	ID           int64   `json:"id"`
	Codigo       string  `json:"codigo"`
	Nombre       string  `json:"nombre"`
	SuperficieHa float64 `json:"superficie_ha"`
	TipoSuelo    string  `json:"tipo_suelo"`
	Campana      string  `json:"campana"`
	Cultivo      string  `json:"cultivo"`
}

// handleListLotes lista los lotes del tenant con la campaña/cultivo de su
// campaña actual. Es solo lectura, como approvals: cualquier rol autenticado
// puede listar.
func (s *Server) handleListLotes(w http.ResponseWriter, r *http.Request) {
	tid, err := tenant.FromContext(r.Context())
	if err != nil {
		// El middleware inyecta el tenant antes de llegar acá: si no está, es
		// un fallo interno de la cadena, no un error del cliente.
		s.log.Error("lotes: tenant ausente en el contexto", "err", err)
		writeJSONErr(w, http.StatusInternalServerError, "internal")
		return
	}

	lotes, err := s.loteStore.ListLotesConCampanaActual(r.Context(), tid)
	if err != nil {
		s.log.Error("lotes: listar", "err", err, "tenant", tid)
		writeJSONErr(w, http.StatusInternalServerError, "internal")
		return
	}

	// make con cap 0: si no hay lotes, el JSON es [] y no null (contrato).
	out := make([]loteOut, 0, len(lotes))
	for _, l := range lotes {
		out = append(out, loteOut{
			ID: l.ID, Codigo: l.Codigo, Nombre: l.Nombre,
			SuperficieHa: l.SuperficieHa, TipoSuelo: l.TipoSuelo,
			Campana: l.CampanaNombre, Cultivo: l.Cultivo,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"lotes": out})
}