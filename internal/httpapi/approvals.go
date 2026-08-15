package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/agro-agent/agro-agent/internal/approval"
)

// approvalOut es la proyección HTTP de una solicitud. Igual que en las tools:
// NUNCA se expone token ni token_hash al cliente que lista; el token solo
// existe en la respuesta de creación de la tool.
type approvalOut struct {
	ID          int64           `json:"id"`
	Action      string          `json:"action"`
	Status      approval.Status `json:"status"`
	Payload     json.RawMessage `json:"payload"`
	ExpiresAt   time.Time       `json:"expires_at"`
	CreatedAt   time.Time       `json:"created_at"`
	ActorUserID int64           `json:"actor_user_id"`
}

// validApprovalStatuses es el vocabulario aceptado por ?status= de la lista.
var validApprovalStatuses = map[string]bool{
	"": true, "pendiente": true, "aprobado": true,
	"rechazado": true, "ejecutado": true, "vencido": true,
}

// handleListApprovals lista las solicitudes del tenant, opcionalmente filtradas
// por estado. Todos los roles autenticados pueden verlas (es solo lectura).
func (s *Server) handleListApprovals(w http.ResponseWriter, r *http.Request) {
	if s.approvals == nil {
		// El slice HITL no está montado (tests de chat): el endpoint existe
		// pero no puede operar. 501, no 500: el servicio no está implementado.
		writeJSONErr(w, http.StatusNotImplemented, "not implemented")
		return
	}
	status := r.URL.Query().Get("status")
	if !validApprovalStatuses[status] {
		writeJSONErr(w, http.StatusBadRequest, "invalid request")
		return
	}

	reqs, err := s.approvals.List(r.Context(), status)
	if err != nil {
		s.log.Error("approvals: listar", "err", err)
		writeJSONErr(w, http.StatusInternalServerError, "internal error")
		return
	}

	out := make([]approvalOut, 0, len(reqs))
	for _, rq := range reqs {
		out = append(out, approvalOut{
			ID: rq.ID, Action: rq.Action, Status: rq.Status, Payload: rq.Payload,
			ExpiresAt: rq.ExpiresAt, CreatedAt: rq.CreatedAt, ActorUserID: rq.ActorUserID,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]any{"approvals": out})
}

// approveRequest es el body de approve/reject: el token que se devolvió al
// crear la solicitud. Nada más (fail-closed con DisallowUnknownFields).
type approveRequest struct {
	Token string `json:"token"`
}

// handleApprove aprueba y ejecuta la solicitud. El error del service se mapea
// a un 409 uniforme ("no aprobable") para los casos de negocio esperables:
// no existe, no está pendiente, venció o el token no matchea. Un atacante no
// debe distinguir por el código HTTP cuál de esos casos pasó.
func (s *Server) handleApprove(w http.ResponseWriter, r *http.Request) {
	if s.approvals == nil {
		// Mismo contrato que la lista: con el slice HITL desmontado, 501.
		writeJSONErr(w, http.StatusNotImplemented, "not implemented")
		return
	}
	id, ok := approvalPathID(w, r)
	if !ok {
		return
	}
	body, err := decodeApproveBody(r)
	if err != nil {
		writeJSONErr(w, http.StatusBadRequest, "invalid request")
		return
	}

	app, err := s.approvals.Approve(r.Context(), id, body.Token)
	if err != nil {
		writeApprovalError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ejecutado", "aplicacion_id": app.ID})
}

// handleReject rechaza la solicitud sin crear aplicación. Mismo contrato de
// token y mismo mapeo de errores que approve.
func (s *Server) handleReject(w http.ResponseWriter, r *http.Request) {
	if s.approvals == nil {
		writeJSONErr(w, http.StatusNotImplemented, "not implemented")
		return
	}
	id, ok := approvalPathID(w, r)
	if !ok {
		return
	}
	body, err := decodeApproveBody(r)
	if err != nil {
		writeJSONErr(w, http.StatusBadRequest, "invalid request")
		return
	}

	if err := s.approvals.Reject(r.Context(), id, body.Token); err != nil {
		writeApprovalError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "rechazado"})
}

// approvalPathID parsea el {id} de la ruta. Un id no numérico es un error del
// cliente (400): jamás llega como string crudo al service.
func approvalPathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSONErr(w, http.StatusBadRequest, "invalid request")
		return 0, false
	}
	return id, true
}

// decodeApproveBody deserializa {"token": "..."} con DisallowUnknownFields:
// un campo extra en el body es un error de contrato, no algo a ignorar.
func decodeApproveBody(r *http.Request) (approveRequest, error) {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var body approveRequest
	if err := dec.Decode(&body); err != nil {
		return approveRequest{}, err
	}
	if strings.TrimSpace(body.Token) == "" {
		return approveRequest{}, errors.New("token vacío")
	}
	return body, nil
}

// writeApprovalError mapea los errores centinela del service a HTTP. Los
// casos de negocio esperables → 409 uniforme; cualquier otro fallo (DB, etc.)
// → 500 con el detalle logueado (nunca enviado al cliente).
func writeApprovalError(w http.ResponseWriter, log *slog.Logger, err error) {
	switch {
	case errors.Is(err, approval.ErrNotFound),
		errors.Is(err, approval.ErrExpired),
		errors.Is(err, approval.ErrNotPending),
		errors.Is(err, approval.ErrInvalidToken):
		writeJSONErr(w, http.StatusConflict, "no aprobable")
	default:
		log.Error("approvals: decisión falló", "err", err)
		writeJSONErr(w, http.StatusInternalServerError, "internal error")
	}
}

// writeJSON escribe una respuesta JSON con su status. Separado para que los
// handlers de éxito no repitan el par header+encode.
func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(payload)
}