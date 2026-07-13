// Package editor serves the Hookway config API (JSON Schema + validation) over
// HTTP for a future config UI. It serves no UI assets and stores no state.
package editor

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/jasongiroux/hookway/internal/apikey"
	"github.com/jasongiroux/hookway/internal/configapi"
)

// maxValidateBytes caps the /validate request body. The editor does not load a
// config, so it applies its own fixed ceiling rather than server.max_body_bytes.
const maxValidateBytes = 5 << 20 // 5 MiB

// Handler returns the routed handler for the config API, guarded by apiKey when
// non-empty. CORS is outermost so OPTIONS preflight (which cannot carry an
// Authorization header) is answered before the auth check runs.
func Handler(apiKey string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /schema", handleSchema)
	mux.HandleFunc("POST /validate", handleValidate)
	return withCORS(apikey.Middleware(apiKey, mux))
}

// NewServer returns an http.Server bound to addr serving the config API, guarded
// by apiKey when non-empty.
func NewServer(addr, apiKey string) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           Handler(apiKey),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}

func handleSchema(w http.ResponseWriter, r *http.Request) {
	data, err := configapi.Schema()
	if err != nil {
		http.Error(w, "schema generation failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/schema+json")
	_, _ = w.Write(data)
}

func handleValidate(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxValidateBytes))
	if err != nil {
		http.Error(w, "request body too large or unreadable", http.StatusBadRequest)
		return
	}
	resp := configapi.Validate(body)
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, resp)
}

// withCORS allows any origin. These endpoints expose no secrets: /schema is
// public and /validate only echoes validation issues.
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
