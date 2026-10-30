package application

import (
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strings"

	"agent-task-bridge/internal/domain"
	"agent-task-bridge/internal/wirevalue"
)

func (s *Service) Handler() http.Handler { return http.HandlerFunc(s.serveHTTP) }
func transport(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(domain.Object{"error": domain.Object{"code": code, "message": code}})
}
func rpcError(w http.ResponseWriter, id any, e error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(domain.Object{"jsonrpc": "2.0", "id": id, "error": errorOf(e)})
}
func (s *Service) serveHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Path == "/ready" {
		if s.healthy.Load() {
			w.WriteHeader(204)
		} else {
			transport(w, 503, "UNAVAILABLE")
		}
		return
	}
	if r.URL.Path != "/rpc" {
		http.NotFound(w, r)
		return
	}
	if r.Method != "POST" {
		w.Header().Set("Allow", "POST")
		transport(w, 405, "TRANSPORT_REJECTED")
		return
	}
	contentType, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if e != nil || contentType != "application/json" {
		transport(w, 415, "TRANSPORT_REJECTED")
		return
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if token == "" || token == r.Header.Get("Authorization") {
		transport(w, 401, "UNAUTHENTICATED")
		return
	}
	// Authenticate before body parsing; repeat it inside the authoritative operation.
	tx, e := s.store.Begin(r.Context(), false)
	if e != nil {
		transport(w, 503, "UNAVAILABLE")
		return
	}
	now, e := s.now(tx, false)
	if e == nil {
		c, ce := loadConfig(tx)
		if ce != nil {
			e = ce
		} else {
			_, e = authn(c, token, now)
		}
	}
	tx.Rollback()
	if e != nil {
		if errorOf(e).Data.Kind == "UNAUTHENTICATED" {
			transport(w, 401, "UNAUTHENTICATED")
		} else {
			transport(w, 503, "UNAVAILABLE")
		}
		return
	}
	if !s.healthy.Load() {
		transport(w, 503, "UNAVAILABLE")
		return
	}
	raw, e := io.ReadAll(io.LimitReader(r.Body, 262145))
	if e != nil {
		rpcError(w, nil, domain.E("PARSE_ERROR"))
		return
	}
	if len(raw) > 262144 {
		transport(w, 413, "TRANSPORT_REJECTED")
		return
	}
	parsed, e := wirevalue.Parse(raw)
	if e != nil {
		rpcError(w, nil, domain.E("PARSE_ERROR"))
		return
	}
	if _, ok := parsed.([]any); ok {
		transport(w, 400, "TRANSPORT_REJECTED")
		return
	}
	obj, ok := parsed.(map[string]any)
	if !ok {
		rpcError(w, nil, domain.E("INVALID_REQUEST"))
		return
	}
	if _, hasID := obj["id"]; !hasID {
		w.WriteHeader(204)
		return
	}
	id, idOK := obj["id"].(string)
	if !idOK || id == "" || len(id) > 128 || obj["jsonrpc"] != "2.0" {
		rpcError(w, nil, domain.E("INVALID_REQUEST"))
		return
	}
	method, ok := obj["method"].(string)
	if !ok {
		rpcError(w, nil, domain.E("INVALID_REQUEST"))
		return
	}
	params, ok := obj["params"].(map[string]any)
	if !ok {
		rpcError(w, id, domain.E("INVALID_PARAMS"))
		return
	}
	def := "Params_" + strings.ReplaceAll(method, ".", "_")
	if !writeMethods[method] && !domain.Contains([]string{"bridge.capabilities", "task.get", "record.get", "task.discover", "snapshot.open", "snapshot.page", "events.open", "events.read", "receipt.get"}, method) {
		rpcError(w, id, domain.E("METHOD_NOT_FOUND"))
		return
	}
	if e = wirevalue.Validate(def, params); e != nil {
		rpcError(w, id, domain.E("INVALID_PARAMS"))
		return
	}
	if e = wirevalue.Validate("Request", obj); e != nil {
		rpcError(w, id, domain.E("INVALID_REQUEST"))
		return
	}
	if method != "bridge.capabilities" && domain.S(domain.Object(params), "authority_id") != s.authority {
		rpcError(w, id, domain.E("AUTHORITY_MISMATCH"))
		return
	}
	var data any
	var replay bool
	if writeMethods[method] {
		data, replay, e = s.command(r.Context(), token, id, method, domain.Object(params))
	} else {
		data, replay, e = s.query(r.Context(), token, method, domain.Object(params))
	}
	if e != nil {
		if errorOf(e).Data.Kind == "UNAUTHENTICATED" {
			transport(w, 401, "UNAUTHENTICATED")
			return
		}
		if errorOf(e).Data.Kind == "UNAVAILABLE" {
			transport(w, 503, "UNAVAILABLE")
			return
		}
		rpcError(w, id, e)
		return
	}
	result := domain.Object{"jsonrpc": "2.0", "id": id, "result": domain.Object{"data": data, "meta": domain.Object{"authority_id": s.authority, "replayed": replay}}}
	if e = wirevalue.Validate("SuccessResponse", result); e != nil {
		rpcError(w, id, domain.E("INTERNAL_ERROR"))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}
