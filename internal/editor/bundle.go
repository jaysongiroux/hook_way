package editor

import (
	"io"
	"net/http"

	"github.com/jaysongiroux/hook_way/internal/configapi"
)

func handleValidateBundle(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, configapi.MaxBundleRequestBytes))
	if err != nil {
		http.Error(w, "bundle request too large or unreadable", http.StatusRequestEntityTooLarge)
		return
	}
	input, err := configapi.DecodeBundleInput(raw)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, configapi.ValidateBundle(input))
}
