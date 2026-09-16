// Package editor serves the Hookway config UI (an embedded static SPA) plus the
// config API (JSON Schema + validation) over HTTP. The SPA is unauthenticated;
// the API is apikey-guarded. It stores no state and loads no config file.
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

// Handler returns the routed handler for the config editor: the SPA at / (no
// auth — the browser needs the HTML/JS before it can present a key) and the
// config API (GET /schema, GET /node-schema, POST /validate) guarded by apiKey when non-empty.
// CORS is outermost so OPTIONS preflight is answered before auth runs.
//
// The API routes are dispatched through a nested mux reached via method-agnostic
// top-level patterns ("/schema", "/node-schema", "/validate"). Registering "GET /schema" and
// "POST /validate" directly alongside the "GET /" SPA catch-all would let the
// catch-all silently absorb the "wrong" method for each API path (e.g. GET
// /validate), since it is the only pattern matching that method — defeating
// net/http's automatic 405 for a mismatched method on a known path.
func Handler(apiKey string) http.Handler {
	api := http.NewServeMux()
	api.Handle("GET /schema", apikey.Middleware(apiKey, http.HandlerFunc(handleSchema)))
	api.Handle("GET /node-schema", apikey.Middleware(apiKey, http.HandlerFunc(handleNodeSchema)))
	api.Handle("POST /validate", apikey.Middleware(apiKey, http.HandlerFunc(handleValidate)))

	mux := http.NewServeMux()
	mux.Handle("/schema", api)
	mux.Handle("/node-schema", api)
	mux.Handle("/validate", api)
	mux.Handle("/", uiHandler())
	return withCORS(mux)
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

func handleNodeSchema(w http.ResponseWriter, r *http.Request) {
	data, err := configapi.NodeSchemas()
	if err != nil {
		http.Error(w, "node schema generation failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
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
