package editor

import (
	"errors"
	"io"
	"net/http"

	"github.com/jaysongiroux/hook_way/internal/uicontract"
)

func handleJSONSampleValidation(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, uicontract.MaxJSONSampleRequestBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, "JSON sample validation request exceeds 8 MiB", http.StatusRequestEntityTooLarge)
		} else {
			http.Error(w, "request body unreadable", http.StatusBadRequest)
		}
		return
	}
	input, err := uicontract.DecodeJSONSampleInput(body)
	if err != nil {
		http.Error(w, "invalid JSON sample validation request", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, uicontract.ValidateJSONSample(input))
}
