package editor

import (
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

// webuiFS holds the built config-UI bundle produced by `make ui`
// (internal/editor/webui/). That directory is entirely build output: only
// .gitkeep is tracked, so this pattern still compiles on a fresh checkout —
// all: includes dot/underscore-prefixed files. When no bundle is staged the
// handler falls back to placeholderHTML.
//
//go:embed all:webui
var webuiFS embed.FS

// placeholderHTML is the tracked page served when the binary was built without
// a staged UI bundle. It lives outside webui/ so `make ui` never dirties a
// tracked file.
//
//go:embed placeholder.html
var placeholderHTML []byte

func init() {
	// Ensure .wasm is served as application/wasm even on systems whose mime
	// table lacks it, so WebAssembly.instantiateStreaming works in the browser.
	_ = mime.AddExtensionType(".wasm", "application/wasm")
}

// uiHandler serves the embedded SPA.
func uiHandler() http.Handler {
	sub, err := fs.Sub(webuiFS, "webui")
	if err != nil {
		panic(err) // embedded FS is fixed at build time; a failure is a bug
	}
	return newUIHandler(sub)
}

// newUIHandler serves the staged SPA from fsys. A request for a file that
// exists is served directly; an extension-less path falls back to index.html so
// client-side routing works. Requests for a missing build artifact 404 rather
// than fall back, because returning HTML for a module script the browser asked
// for yields a blank page instead of a visible error. When fsys holds no
// index.html at all, every request gets the placeholder page.
func newUIHandler(fsys fs.FS) http.Handler {
	if _, err := fs.Stat(fsys, "index.html"); err != nil {
		return http.HandlerFunc(servePlaceholder)
	}
	files := http.FileServer(http.FS(fsys))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := strings.TrimPrefix(r.URL.Path, "/")
		if clean == "" {
			files.ServeHTTP(w, r)
			return
		}
		if _, err := fs.Stat(fsys, clean); err != nil {
			if path.Ext(clean) != "" {
				http.NotFound(w, r)
				return
			}
			r = r.Clone(r.Context())
			r.URL.Path = "/"
			files.ServeHTTP(w, r)
			return
		}
		files.ServeHTTP(w, r)
	})
}

// servePlaceholder explains that the UI bundle was not staged at build time.
func servePlaceholder(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(placeholderHTML)
}
