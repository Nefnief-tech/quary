// Package api is the panel's REST API: one stdlib mux, one authn middleware,
// one authz middleware. Handlers stay thin; permission checks are declared
// per route in router.go.
package api

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
)

const maxBodyBytes = 1 << 20 // 1 MiB

// queryValue returns the first value of a query parameter. Handlers use
// this instead of url.Values.Get so the raw map is the single access path.
func queryValue(r *http.Request, key string) string {
	values := r.URL.Query()
	if len(values[key]) == 0 {
		return ""
	}
	return values[key][0]
}

type errorResponse struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

func apiError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorResponse{Error: msg})
}

// decode strictly decodes a JSON request body into T. On failure it writes
// the error response and returns false.
func decode[T any](w http.ResponseWriter, r *http.Request) (T, bool) {
	var out T
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			apiError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return out, false
		}
		apiError(w, http.StatusBadRequest, "invalid JSON body")
		return out, false
	}
	return out, true
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
