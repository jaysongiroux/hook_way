// Package editor serves the Hookway config UI (an embedded static SPA) plus the
// config API (JSON Schema + validation) over HTTP. The SPA is unauthenticated;
// the API is apikey-guarded. It stores no state and loads no config file.
package editor

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/jaysongiroux/hook_way/internal/apikey"
	"github.com/jaysongiroux/hook_way/internal/configapi"
	replayconfigapi "github.com/jaysongiroux/hook_way/internal/replay/configapi"
)

// maxValidateBytes caps both validation request bodies. The editor does not
// load a config, so it applies its own fixed ceiling rather than
// server.max_body_bytes.
const maxValidateBytes = 5 << 20 // 5 MiB

// Handler returns the routed handler for the config editor: the SPA at / (no
// auth — the browser needs the HTML/JS before it can present a key) and the
// config APIs (GET /schema, GET /node-schema, POST /validate,
// GET /replay-schema, and POST /validate-replay) guarded by apiKey when
// non-empty.
// CORS is outermost so OPTIONS preflight is answered before auth runs.
//
// The API routes are dispatched through a nested mux reached via method-agnostic
// top-level patterns. Registering method-specific API patterns directly
// alongside the "GET /" SPA catch-all would let the catch-all silently absorb
// a wrong method for a known API path, defeating net/http's automatic 405.
func Handler(apiKey string) http.Handler {
	api := http.NewServeMux()
	handlers := map[string]http.HandlerFunc{"coreSchema": handleSchema, "nodeSchemas": handleNodeSchema, "validateCore": validationHandler(configapi.Validate), "validateBundle": handleValidateBundle, "replaySchema": handleReplaySchema, "validateReplay": validationHandler(replayconfigapi.Validate), "contractInfo": handleContract, "jsonSampleValidation": handleJSONSampleValidation}
	mux := http.NewServeMux()
	for _, route := range Routes() {
		handler := handlers[route.OperationID]
		if handler == nil {
			panic("editor route has no handler: " + route.OperationID)
		}
		api.Handle(route.Pattern(), apikey.Middleware(apiKey, handler))
		mux.Handle(route.Path, api)
	}
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

// validationHandler shares bounded YAML transport while keeping each
// validator's typed response and configuration semantics separate.
func validationHandler[T any](validate func([]byte) T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxValidateBytes))
		if err != nil {
			http.Error(w, "request body too large or unreadable", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		writeJSON(w, validate(body))
	}
}

func handleReplaySchema(w http.ResponseWriter, r *http.Request) {
	data, err := replayconfigapi.Schema()
	if err != nil {
		http.Error(w, "replay schema generation failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/schema+json")
	_, _ = w.Write(data)
}

// withCORS allows any origin. These endpoints never load server configuration
// or environment secrets. Explicit API-key guards still apply to every route.
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
