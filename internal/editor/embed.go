package editor

import (
	"embed"
	"io/fs"
	"mime"
	"net/http"
)

// webuiFS holds the built config-UI bundle staged by `make ui`
// (internal/editor/webui/). On a fresh checkout that is only the tracked
// placeholder index.html; `make ui` overwrites it with the real SPA plus its
// assets and the WASM validator. all: includes dot/underscore-prefixed files.
//
//go:embed all:webui
var webuiFS embed.FS

func init() {
	// Ensure .wasm is served as application/wasm even on systems whose mime
	// table lacks it, so WebAssembly.instantiateStreaming works in the browser.
	_ = mime.AddExtensionType(".wasm", "application/wasm")
}

// uiHandler serves the embedded SPA. Requests for a file that exists are served
// directly; anything else falls back to index.html so client-side routing works.
func uiHandler() http.Handler {
	sub, err := fs.Sub(webuiFS, "webui")
	if err != nil {
		panic(err) // embedded FS is fixed at build time; a failure is a bug
	}
	files := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if p == "/" {
			files.ServeHTTP(w, r)
			return
		}
		clean := p
		if len(clean) > 0 && clean[0] == '/' {
			clean = clean[1:]
		}
		if _, statErr := fs.Stat(sub, clean); statErr != nil {
			r = r.Clone(r.Context())
			r.URL.Path = "/"
			files.ServeHTTP(w, r)
			return
		}
		files.ServeHTTP(w, r)
	})
}
