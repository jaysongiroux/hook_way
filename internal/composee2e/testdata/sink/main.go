// Command sink stands in for the dev stack's notify-sink during the compose
// end-to-end test. It takes hashicorp/http-echo's flags, so it drops into the
// service unchanged, but it also remembers the envelope id of every POST and
// lists them at GET /received.
package main

import (
	"encoding/json"
	"flag"
	"io"
	"log"
	"net/http"
	"sync"
	"time"
)

func main() {
	listen := flag.String("listen", ":8080", "address to listen on")
	text := flag.String("text", "ok", "response body")
	flag.Parse()

	var mu sync.Mutex
	received := map[string]int{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /", func(w http.ResponseWriter, r *http.Request) {
		var env struct {
			ID string `json:"id"`
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 16<<20))
		if err != nil || json.Unmarshal(body, &env) != nil || env.ID == "" {
			http.Error(w, "want a JSON envelope with an id", http.StatusBadRequest)
			return
		}
		mu.Lock()
		received[env.ID]++
		mu.Unlock()
		_, _ = io.WriteString(w, *text)
	})
	mux.HandleFunc("GET /received", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(received)
	})
	srv := &http.Server{Addr: *listen, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(srv.ListenAndServe())
}
